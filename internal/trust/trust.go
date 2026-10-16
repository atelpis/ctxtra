// Package trust decides whether ctxtra may read a file from disk.
//
// A file is trusted if it is outside git, in a repository with no remote, or
// under a trusted-path source. A file in a repository that has a remote and is
// not a trusted source is skipped: its content may come from someone else. A
// file is trusted only if every repository around it is trusted, and every
// symlink on the way to it too.
//
// Repositories are found by reading .git and the git config files, without
// running git. Anything that can't be read or isn't supported counts as
// untrusted.
package trust

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"syscall"

	"github.com/atelpis/ctxtra/internal/fsutil"
	"github.com/atelpis/ctxtra/internal/gitrepo"
	"github.com/atelpis/ctxtra/internal/paths"
)

// maxLinks is how many symlinks one path may pass, as in Linux.
const maxLinks = 40

// maxGitFile limits each .git file and git config file that is read, and
// maxGitBytes all of them together for one Policy.
const (
	maxGitFile  = 1 << 20
	maxGitBytes = 4 << 20
)

// Root is a trusted-path source: a folder whose files are trusted.
type Root struct {
	Name string
	Path string // absolute, ~ expanded
}

// Policy checks paths against the trusted roots. It caches what it learns
// about each folder, so one Policy is used for one session.
type Policy struct {
	roots []rootInfo
	deny  []rootInfo
	dirs  map[string]dirInfo
	rd    *fsutil.Reader // git metadata, with its own limits
}

type rootInfo struct {
	Root
	fi os.FileInfo // nil when the folder doesn't exist
}

// dirInfo is what a folder says about trust.
type dirInfo struct {
	root bool   // the folder is a trusted root
	deny bool   // the folder is denied
	repo bool   // the folder is the top of a git repository
	err  error  // set when the repository can't be read
	url  string // a remote of the repository, normalized; "" when it has none
	has  bool   // the repository has a remote
}

// New returns a Policy for the given trusted roots. Files under a denied
// root are never trusted, wherever they are.
func New(roots []Root, deny ...Root) *Policy {
	p := &Policy{dirs: map[string]dirInfo{}, rd: fsutil.NewReader(maxGitFile, maxGitBytes)}
	for _, r := range roots {
		fi, _ := os.Stat(r.Path)
		p.roots = append(p.roots, rootInfo{r, fi})
	}
	for _, r := range deny {
		fi, _ := os.Stat(r.Path)
		p.deny = append(p.deny, rootInfo{r, fi})
	}
	return p
}

// DeniedError is a file under a denied root.
type DeniedError struct {
	Path string
	Root Root
}

func (e *DeniedError) Error() string {
	return fmt.Sprintf("%s is in %s, which can't be referenced by its path; skipped", paths.Tilde(e.Path), e.Root.Name)
}

// Error is why a file is not trusted.
type Error struct {
	Path   string // the file
	Via    string // a symlink on the way, or where it leads, when that is what failed
	Repo   string // the repository top
	Remote string // a remote of the repository, normalized; "" when it can't be read
	Err    error  // set when the repository can't be read
}

func (e *Error) Error() string {
	what := paths.Tilde(e.Path) + " is"
	if e.Via != "" {
		what = fmt.Sprintf("%s leads through a symlink to %s, which is", paths.Tilde(e.Path), paths.Tilde(e.Via))
	}
	if e.Err != nil {
		return fmt.Sprintf("%s in the git repository %s, which ctxtra can't check (%v); skipped",
			what, paths.Tilde(e.Repo), e.Err)
	}
	return fmt.Sprintf("%s in %s, which has the remote %s and is not a trusted source; skipped. "+
		"If the repository is yours: ctxtra add --trust %s; to pin it, add the file with ctxtra add",
		what, paths.Tilde(e.Repo), e.Remote, paths.Tilde(e.Repo))
}

// Check returns nil if path may be read, and an *Error or a symlink error if
// not. path is absolute. Every symlink on the way to the file is checked at
// its own location, and then the file itself, so that a link in an untrusted
// repository can't lead to a trusted file, and the other way round.
func (p *Policy) Check(path string) error {
	if p == nil {
		p = New(nil)
	}
	if !filepath.IsAbs(path) {
		return fmt.Errorf("%s: not an absolute path; skipped", path)
	}
	links, final, err := resolveLinks(path)
	if err != nil {
		return fmt.Errorf("%s: %w; skipped", paths.Tilde(path), err)
	}
	for _, l := range append(links, final) {
		if err := p.checkOne(l); err != nil {
			if e, ok := err.(*Error); ok && l != path {
				e.Path, e.Via = path, l
			}
			if e, ok := err.(*DeniedError); ok {
				e.Path = path
			}
			return err
		}
	}
	return nil
}

// checkOne checks one location: the folders above path. A folder that is a
// trusted root trusts the repository it is in, up to the next repository
// top; a repository nested below a root needs its own root.
func (p *Policy) checkOne(path string) error {
	trusted := false
	for dir := filepath.Dir(path); ; dir = filepath.Dir(dir) {
		d := p.dir(dir)
		if d.deny {
			return &DeniedError{Path: path, Root: p.deniedRoot(dir)}
		}
		trusted = trusted || d.root
		if d.repo {
			switch {
			case d.err != nil:
				return &Error{Path: path, Repo: dir, Err: d.err}
			case d.has && !trusted:
				return &Error{Path: path, Repo: dir, Remote: d.url}
			}
			trusted = false
		}
		if filepath.Dir(dir) == dir {
			return nil
		}
	}
}

func (p *Policy) dir(dir string) dirInfo {
	if d, ok := p.dirs[dir]; ok {
		return d
	}
	var d dirInfo
	if fi, err := os.Stat(dir); err == nil {
		same := func(r rootInfo) bool { return r.fi != nil && os.SameFile(fi, r.fi) }
		d.root = slices.ContainsFunc(p.roots, same)
		d.deny = slices.ContainsFunc(p.deny, same)
	}
	switch _, err := os.Lstat(filepath.Join(dir, ".git")); {
	case err == nil:
		d.repo = true
		d.url, d.has, d.err = p.remotes(dir)
	case !notFound(err):
		d.repo, d.err = true, err
	}
	p.dirs[dir] = d
	return d
}

func (p *Policy) deniedRoot(dir string) Root {
	fi, _ := os.Stat(dir)
	for _, r := range p.deny {
		if fi != nil && r.fi != nil && os.SameFile(fi, r.fi) {
			return r.Root
		}
	}
	return Root{Name: dir}
}

func notFound(err error) bool {
	return errors.Is(err, fs.ErrNotExist) || errors.Is(err, syscall.ENOTDIR)
}

// resolveLinks follows path the way the system does and returns the symlinks
// it passes, each at its own location, and the path it ends at. When a part
// of the path doesn't exist, the rest is joined as it is: reading the file
// then reports it missing.
func resolveLinks(path string) (links []string, final string, err error) {
	vol := filepath.VolumeName(path)
	cur := vol + string(filepath.Separator)
	pending := strings.Split(filepath.ToSlash(path[len(vol):]), "/")
	for len(pending) > 0 {
		c := pending[0]
		pending = pending[1:]
		switch c {
		case "", ".":
			continue
		case "..":
			cur = filepath.Dir(cur)
			continue
		}
		next := filepath.Join(cur, c)
		fi, err := os.Lstat(next)
		if err != nil {
			return links, filepath.Join(append([]string{next}, pending...)...), nil
		}
		if fi.Mode()&fs.ModeSymlink == 0 {
			cur = next
			continue
		}
		if len(links) == maxLinks {
			return nil, "", errors.New("too many symlinks")
		}
		links = append(links, next)
		target, err := os.Readlink(next)
		if err != nil {
			return nil, "", err
		}
		if filepath.IsAbs(target) {
			cur = filepath.VolumeName(target) + string(filepath.Separator)
			target = target[len(filepath.VolumeName(target)):]
		}
		pending = append(strings.Split(filepath.ToSlash(target), "/"), pending...)
	}
	return links, cur, nil
}

// remotes reads the git config of the repository whose top is dir, and
// reports a remote of it. .git is a folder, or a file with the line
// "gitdir: <path>" (worktrees, submodules). A worktree's git folder has a
// commondir file that points to the shared one, which holds the config.
func (p *Policy) remotes(dir string) (url string, has bool, err error) {
	gitDir := filepath.Join(dir, ".git")
	fi, err := os.Stat(gitDir)
	if err != nil {
		return "", false, err
	}
	if !fi.IsDir() {
		b, err := p.read(gitDir)
		if err != nil {
			return "", false, err
		}
		target, ok := strings.CutPrefix(strings.TrimSpace(string(b)), "gitdir: ")
		if !ok {
			return "", false, errors.New(".git is a file without a gitdir line")
		}
		gitDir = paths.Resolve(dir, target)
	}
	common := gitDir
	switch b, err := p.read(filepath.Join(gitDir, "commondir")); {
	case err == nil:
		common = paths.Resolve(gitDir, strings.TrimSpace(string(b)))
	case !notFound(err):
		return "", false, err
	}
	files := []string{filepath.Join(common, "config"), filepath.Join(gitDir, "config.worktree")}
	for i, f := range files {
		b, err := p.read(f)
		if i > 0 && notFound(err) {
			continue // config.worktree is optional
		}
		if err != nil {
			return "", false, err
		}
		u, h, err := parseRemotes(b)
		if err != nil {
			return "", false, fmt.Errorf("%s: %w", paths.Tilde(f), err)
		}
		if h && !has {
			url, has = u, true
		}
	}
	return url, has, nil
}

// read reads a git metadata file within the Policy's budget.
func (p *Policy) read(name string) ([]byte, error) {
	b, err := p.rd.Read(name)
	if errors.Is(err, fsutil.ErrTotalLimit) {
		err = errors.New("too much git metadata to read")
	}
	return b, err
}

var (
	reSection = regexp.MustCompile(`^\[\s*([A-Za-z0-9.-]+)(\s+"(?:[^"\\\n]|\\.)*")?\s*\]`)
	reKey     = regexp.MustCompile(`^([A-Za-z][A-Za-z0-9-]*)\s*(?:$|[#;]|=(.*))`)
)

// parseRemotes reports whether a git config file declares a remote, and the
// first remote URL in it, normalized. Any [remote ...] section counts, also
// one without a url. The file is checked line by line against a strict
// subset of git's syntax, so that a header can't hide where the parser
// doesn't look: a line it doesn't understand, a line continuation, and
// include sections (they read other files) are errors.
func parseRemotes(b []byte) (url string, has bool, err error) {
	b = bytes.TrimPrefix(b, []byte("\ufeff"))
	section := ""
	for n, line := range strings.Split(string(b), "\n") {
		line = strings.Trim(line, " \t\r")
		bad := func(why string) (string, bool, error) {
			return "", false, fmt.Errorf("line %d: %s", n+1, why)
		}
		if strings.HasSuffix(line, "\\") {
			return bad("line continuations are not supported")
		}
		if line == "" || line[0] == '#' || line[0] == ';' {
			continue
		}
		if line[0] == '[' {
			m := reSection.FindStringSubmatch(line)
			if m == nil {
				return bad("unreadable section header")
			}
			section, _, _ = strings.Cut(strings.ToLower(m[1]), ".")
			if section == "include" || section == "includeif" {
				return bad("include sections are not supported")
			}
			has = has || section == "remote"
			line = strings.TrimLeft(line[len(m[0]):], " \t") // a key can follow on the same line
			if line == "" || line[0] == '#' || line[0] == ';' {
				continue
			}
		}
		m := reKey.FindStringSubmatch(line)
		if m == nil || section == "" || !validValue(m[2]) {
			return bad("unreadable line")
		}
		if section == "remote" && url == "" && strings.EqualFold(m[1], "url") {
			url = gitrepo.NormalizeRemote(value(m[2]))
		}
	}
	if has && url == "" {
		url = "(unknown)"
	}
	return url, has, nil
}

// validValue reports whether v is a value git reads: quoted parts closed, and
// only the escapes git knows, up to a comment.
func validValue(v string) bool {
	quoted := false
	for i := 0; i < len(v); i++ {
		switch c := v[i]; {
		case c == '\\':
			i++
			if i == len(v) || !strings.ContainsRune(`"\\ntb`, rune(v[i])) {
				return false
			}
		case c == '"':
			quoted = !quoted
		case !quoted && (c == '#' || c == ';'):
			return true
		}
	}
	return !quoted
}

// value is a config value without a comment and quotes, good enough to show a
// remote URL.
func value(v string) string {
	v = strings.TrimSpace(v)
	if rest, ok := strings.CutPrefix(v, `"`); ok {
		v, _, _ = strings.Cut(rest, `"`)
		return v
	}
	if i := strings.IndexAny(v, "#;"); i >= 0 {
		v = v[:i]
	}
	return strings.TrimSpace(v)
}
