// Package source fetches git sources and keeps the accepted commits of each
// one in ctxtra's store.
//
// Each source has a bare repository in ~/.ctxtra/git and, for each accepted
// commit, a generation in ~/.ctxtra/store: the commit's regular files, written
// once and never changed. Sessions read only generations; only the commands
// that fetch (add, sync) run git with network access.
//
// Git runs as the user's git, so ssh keys and credential helpers work, but
// only with the https and ssh transports, without hooks or submodules, and
// without checking anything out: .gitattributes filters never run.
package source

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"

	"golang.org/x/text/cases"
	"golang.org/x/text/unicode/norm"

	"github.com/atelpis/ctxtra/internal/fsutil"
	"github.com/atelpis/ctxtra/internal/gitrepo"
	"github.com/atelpis/ctxtra/internal/paths"
	"github.com/atelpis/ctxtra/internal/term"
)

// Limits for one commit. Over any of them, the whole commit is refused.
const (
	MaxEntries    = 10000 // tree entries, symlinks and submodules included
	MaxFileBytes  = 10 << 20
	MaxTotalBytes = 100 << 20
	maxPathBytes  = 1024
	maxPathDepth  = 64
)

// maxRepoBytes limits a source's repository, history included. A variable
// for tests.
var maxRepoBytes int64 = 1 << 30

// Limits on what one git command prints.
const (
	maxOutput     = 16 << 20
	maxDiffOutput = 32 << 20
	maxStderr     = 64 << 10
)

// Timeouts are variables for tests.
var (
	fetchTimeout = 10 * time.Minute
	readTimeout  = 2 * time.Minute
)

var (
	reCommit = regexp.MustCompile(`^([0-9a-f]{40}|[0-9a-f]{64})$`)
	reBranch = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9._/-]*$`)
	reSCP    = regexp.MustCompile(`^([A-Za-z0-9._-]+@)?[A-Za-z0-9][A-Za-z0-9.-]*:[^/]`)
	reHost   = regexp.MustCompile(`^(https|ssh)://([^/@]+@)?[A-Za-z0-9\[]`)
)

// ValidCommit reports whether s is a full commit ID: 40 hex digits for a
// SHA-1 repository, 64 for SHA-256, in lower case.
func ValidCommit(s string) bool { return reCommit.MatchString(s) }

// Short is the first 7 digits of a commit ID, for messages.
func Short(commit string) string { return commit[:min(7, len(commit))] }

// CheckURL accepts https:// and ssh:// URLs and the scp-like ssh form
// user@host:path. Other transports, local paths, and anything that git could
// read as an option are refused.
func CheckURL(u string) error {
	switch {
	case u == "" || strings.HasPrefix(u, "-") || strings.ContainsFunc(u, unicode.IsControl) || strings.ContainsAny(u, " \t"):
	case reHost.MatchString(u):
		return nil
	case !strings.Contains(u, "://") && reSCP.MatchString(u):
		return nil
	}
	return fmt.Errorf("%q is not a git URL ctxtra can fetch: use https://, ssh:// or user@host:path", u)
}

// CheckBranch accepts ordinary branch names.
func CheckBranch(b string) error {
	if !reBranch.MatchString(b) || strings.Contains(b, "..") || strings.Contains(b, "//") ||
		strings.HasSuffix(b, "/") || strings.HasSuffix(b, ".") || strings.HasSuffix(b, ".lock") || strings.Contains(b, "/.") {
		return fmt.Errorf("%q is not a branch name ctxtra accepts", b)
	}
	return nil
}

// ID names a source's data in the store. It comes from the URL, so a source
// whose URL changes never uses data of the old one.
func ID(url string) string {
	sum := sha256.Sum256([]byte("git\x00" + url))
	return hex.EncodeToString(sum[:8])
}

// StoreDir holds the generations; GitDir the bare repositories.
func StoreDir() string { return filepath.Join(paths.StateDir(), "store") }
func GitDir() string   { return filepath.Join(paths.StateDir(), "git") }

// GenDir is the generation of commit: a folder with the commit's files.
func GenDir(url, commit string) string { return filepath.Join(StoreDir(), ID(url), commit) }

// Has reports whether the generation of commit is in the store. A generation
// is published by renaming a complete folder, so one that exists is complete.
func Has(url, commit string) bool {
	st, err := os.Stat(GenDir(url, commit))
	return err == nil && st.IsDir()
}

// Repo is the bare repository of one source.
type Repo struct {
	URL string
	dir string
}

// Open returns the repository of url. It is created on the first fetch.
func Open(url string) *Repo {
	return &Repo{URL: url, dir: filepath.Join(GitDir(), ID(url)+".git")}
}

// options keep git from running anything from the repository or the
// remote: no hooks, no fsmonitor, no submodules, no automatic maintenance.
var options = []string{
	"-c", "core.hooksPath=" + os.DevNull, "-c", "core.fsmonitor=false",
	"-c", "gc.auto=0", "-c", "maintenance.auto=false",
	"-c", "fetch.recurseSubmodules=false", "-c", "submodule.recurse=false",
	"-c", "protocol.file.allow=never", "-c", "protocol.ext.allow=never",
}

// git runs git with args. With the repository, it runs on it; without, in
// the git folder of the store, where git finds no repository. Its output
// may be up to maxOutput bytes.
func (r *Repo) git(ctx context.Context, repo bool, stdin io.Reader, args ...string) ([]byte, error) {
	return r.gitMax(ctx, repo, stdin, maxOutput, args...)
}

// errOutput stops git when it prints more than allowed: the write fails, so
// git's output pipe closes.
var errOutput = errors.New("too much output")

// capped is a buffer of at most max bytes. With drop, it drops what doesn't
// fit; else a write that doesn't fit fails with errOutput. The buffer is a
// field, not embedded: exec would use an embedded bytes.Buffer's ReadFrom,
// which doesn't go through Write.
type capped struct {
	buf  bytes.Buffer
	max  int
	drop bool
	over bool
}

func (c *capped) Write(p []byte) (int, error) {
	if c.buf.Len()+len(p) <= c.max {
		return c.buf.Write(p)
	}
	c.over = true
	if c.drop {
		c.buf.Write(p[:c.max-c.buf.Len()])
		return len(p), nil
	}
	return 0, errOutput
}

func (r *Repo) gitMax(ctx context.Context, repo bool, stdin io.Reader, max int, args ...string) ([]byte, error) {
	all := append([]string{}, options...)
	if repo {
		all = append(all, "--git-dir="+r.dir)
	}
	cmd := exec.CommandContext(ctx, "git", append(all, args...)...)
	if err := os.MkdirAll(GitDir(), 0o700); err != nil {
		return nil, err
	}
	cmd.Dir = GitDir()
	cmd.Env = append(gitrepo.CleanEnv(),
		"GIT_ALLOW_PROTOCOL=https:ssh",
		"GIT_NO_LAZY_FETCH=1",
		"GIT_CEILING_DIRECTORIES="+filepath.Dir(GitDir()),
		"GIT_EXTERNAL_DIFF=",
		"GIT_PAGER=cat",
	)
	cmd.Stdin = stdin
	stdout := &capped{max: max}
	stderr := &capped{max: maxStderr, drop: true}
	cmd.Stdout, cmd.Stderr = stdout, stderr
	cmd.WaitDelay = time.Second
	err := cmd.Run()
	out := stdout.buf.Bytes()
	if err != nil {
		msg := strings.TrimSpace(Sanitize(stderr.buf.String()))
		switch {
		case stdout.over:
			msg = fmt.Sprintf("printed more than %d bytes", max)
		case errors.Is(context.Cause(ctx), errRepoSize):
			return nil, fmt.Errorf("git %s: %w", args[0], errRepoSize)
		case ctx.Err() != nil:
			msg = "timed out"
		}
		if msg == "" {
			msg = err.Error()
		}
		return nil, fmt.Errorf("git %s: %s", args[0], msg)
	}
	return out, nil
}

// DefaultBranch is the branch the remote's HEAD points to.
func (r *Repo) DefaultBranch(ctx context.Context) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, fetchTimeout)
	defer cancel()
	out, err := r.git(ctx, false, nil, "ls-remote", "--symref", "--", r.URL, "HEAD")
	if err != nil {
		return "", err
	}
	for line := range strings.SplitSeq(string(out), "\n") {
		if ref, ok := strings.CutPrefix(line, "ref: refs/heads/"); ok {
			b, _, _ := strings.Cut(ref, "\t")
			if err := CheckBranch(b); err != nil {
				return "", err
			}
			return b, nil
		}
	}
	return "", fmt.Errorf("%s has no default branch; choose one with --branch", r.URL)
}

// ensure creates the bare repository with the remote's object format. The
// format is read from the length of the IDs the remote lists.
func (r *Repo) ensure(ctx context.Context) error {
	if _, err := os.Stat(filepath.Join(r.dir, "HEAD")); err == nil {
		return nil
	}
	out, err := r.git(ctx, false, nil, "ls-remote", "--", r.URL)
	if err != nil {
		return err
	}
	format := ""
	if id, _, ok := strings.Cut(string(out), "\t"); ok {
		switch len(id) {
		case 40:
			format = "sha1"
		case 64:
			format = "sha256"
		}
	}
	if format == "" {
		return fmt.Errorf("%s has no branches, or an object format ctxtra doesn't support", r.URL)
	}
	// A fresh folder, renamed into place, so a failed init leaves nothing.
	tmp, err := os.MkdirTemp(GitDir(), ".init-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	if _, err := r.git(ctx, false, nil, "init", "-q", "--bare", "--template=", "--object-format="+format, tmp); err != nil {
		return err
	}
	if err := os.Rename(tmp, r.dir); err != nil && !Exists(r.dir) {
		return err
	}
	return nil
}

// Exists reports whether path exists.
func Exists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}

// FetchBranch fetches branch into a private ref and returns the commit it
// points to.
func (r *Repo) FetchBranch(ctx context.Context, branch string) (string, error) {
	if err := CheckBranch(branch); err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(ctx, fetchTimeout)
	defer cancel()
	unlock, err := r.lock()
	if err != nil {
		return "", err
	}
	defer unlock()
	if err := r.ensure(ctx); err != nil {
		return "", err
	}
	// A ref of its own: another ctxtra may fetch another branch at the same
	// time.
	ref := "refs/ctxtra/fetched/" + strconv.FormatInt(time.Now().UnixNano(), 36) + "-" + strconv.Itoa(os.Getpid())
	if err := r.fetch(ctx, "+refs/heads/"+branch+":"+ref); err != nil {
		return "", err
	}
	commit, err := r.commit(ctx, ref)
	r.git(ctx, true, nil, "update-ref", "-d", ref)
	return commit, err
}

// errRepoSize is a fetch that makes the repository larger than
// maxRepoBytes.
var errRepoSize = errors.New("the repository with its history is larger than ctxtra's limit")

// lock keeps other ctxtra processes from fetching into the repository at
// the same time: git fails on the locks of concurrent ref updates.
func (r *Repo) lock() (func(), error) {
	return fsutil.Lock(r.dir + ".lock")
}

// fetch fetches refspec from the URL, and stops git when the repository
// grows over maxRepoBytes.
func (r *Repo) fetch(ctx context.Context, refspec string) error {
	limit := maxRepoBytes
	ctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	done, stopped := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(stopped)
		t := time.NewTicker(200 * time.Millisecond)
		defer t.Stop()
		for {
			select {
			case <-done:
				return
			case <-t.C:
				if dirSize(r.dir) > limit {
					cancel(errRepoSize)
					return
				}
			}
		}
	}()
	_, err := r.git(ctx, true, nil, "fetch", "-q", "--no-tags", "--no-write-fetch-head", "--no-recurse-submodules",
		"--", r.URL, refspec)
	close(done)
	<-stopped
	if err == nil && dirSize(r.dir) > limit {
		err = fmt.Errorf("git fetch: %w", errRepoSize)
	}
	return err
}

// dirSize is the size of the files under dir.
func dirSize(dir string) int64 {
	var n int64
	filepath.WalkDir(dir, func(_ string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			if fi, err := d.Info(); err == nil {
				n += fi.Size()
			}
		}
		return nil
	})
	return n
}

// FetchCommit fetches exactly commit. It fails when the remote doesn't have
// it any more, for example after a force-push.
func (r *Repo) FetchCommit(ctx context.Context, commit string) error {
	if !ValidCommit(commit) {
		return fmt.Errorf("%q is not a full commit ID", commit)
	}
	ctx, cancel := context.WithTimeout(ctx, fetchTimeout)
	defer cancel()
	unlock, err := r.lock()
	if err != nil {
		return err
	}
	defer unlock()
	if err := r.ensure(ctx); err != nil {
		return err
	}
	if c, err := r.commit(ctx, commit); err == nil && c == commit {
		return nil // already here
	}
	if err := r.fetch(ctx, commit); err != nil {
		return fmt.Errorf("can't fetch commit %s (removed by a force-push, or never pushed?): %w", Short(commit), err)
	}
	c, err := r.commit(ctx, commit)
	if err != nil || c != commit {
		return fmt.Errorf("%s fetched, but it isn't the commit %s", r.URL, commit)
	}
	return nil
}

// commit resolves rev to a commit ID in the repository.
func (r *Repo) commit(ctx context.Context, rev string) (string, error) {
	out, err := r.git(ctx, true, nil, "rev-parse", "--verify", "--quiet", "--end-of-options", rev+"^{commit}")
	if err != nil {
		return "", fmt.Errorf("%s is not a commit in %s", rev, r.URL)
	}
	c := strings.TrimSpace(string(out))
	if !ValidCommit(c) {
		return "", fmt.Errorf("unexpected commit ID %q", c)
	}
	return c, nil
}

// IsAncestor reports whether old is an ancestor of commit, so that commit
// continues the accepted history.
func (r *Repo) IsAncestor(ctx context.Context, old, commit string) bool {
	ctx, cancel := context.WithTimeout(ctx, readTimeout)
	defer cancel()
	_, err := r.git(ctx, true, nil, "merge-base", "--is-ancestor", "--end-of-options", old, commit)
	return err == nil
}

// Diff is the diff between two commits, for review: no external diff
// drivers, no textconv, no color, control characters escaped.
func (r *Repo) Diff(ctx context.Context, old, commit string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, readTimeout)
	defer cancel()
	out, err := r.gitMax(ctx, true, nil, maxDiffOutput, "diff", "--no-ext-diff", "--no-textconv", "--no-color", "--find-renames",
		"--end-of-options", old, commit)
	if err != nil {
		return "", err
	}
	return Sanitize(string(out)), nil
}

// Entry is a regular file in a commit.
type Entry struct {
	Path string // slash-separated, relative to the repository top
	OID  string
	Size int64
	Exec bool
}

// Skipped is a tree entry that isn't a regular file.
type Skipped struct {
	Path, Kind string // Kind: "symlink" or "submodule"
}

// Tree lists the regular files of commit, checks their paths and the
// limits, and lists the symlinks and submodules it leaves out.
func (r *Repo) Tree(ctx context.Context, commit string) ([]Entry, []Skipped, error) {
	ctx, cancel := context.WithTimeout(ctx, readTimeout)
	defer cancel()
	out, err := r.git(ctx, true, nil, "ls-tree", "-r", "-z", "--long", "--full-tree", "--end-of-options", commit)
	if err != nil {
		return nil, nil, err
	}
	var entries []Entry
	var skipped []Skipped
	var total int64
	names := names{}
	n := 0
	for rec := range strings.SplitSeq(strings.TrimSuffix(string(out), "\x00"), "\x00") {
		if rec == "" {
			continue
		}
		if n++; n > MaxEntries {
			return nil, nil, fmt.Errorf("more than %d files", MaxEntries)
		}
		meta, path, ok := strings.Cut(rec, "\t")
		f := strings.Fields(meta)
		if !ok || len(f) != 4 {
			return nil, nil, fmt.Errorf("unexpected ls-tree output %q", Sanitize(rec))
		}
		if err := CheckPath(path); err != nil {
			return nil, nil, err
		}
		if err := names.add(path); err != nil {
			return nil, nil, err
		}
		switch f[0] {
		case "120000":
			skipped = append(skipped, Skipped{path, "symlink"})
			continue
		case "160000":
			skipped = append(skipped, Skipped{path, "submodule"})
			continue
		case "100644", "100755", "100664":
		default:
			return nil, nil, fmt.Errorf("%s: unexpected mode %s", Sanitize(path), f[0])
		}
		size, err := strconv.ParseInt(f[3], 10, 64)
		if err != nil {
			return nil, nil, fmt.Errorf("%s: unexpected size %q", Sanitize(path), f[3])
		}
		total += size
		switch {
		case size > MaxFileBytes:
			return nil, nil, fmt.Errorf("%s is larger than %d bytes", Sanitize(path), MaxFileBytes)
		case total > MaxTotalBytes:
			return nil, nil, fmt.Errorf("the files are larger than %d bytes together", MaxTotalBytes)
		}
		entries = append(entries, Entry{Path: path, OID: f[2], Size: size, Exec: f[0] == "100755"})
	}
	return entries, skipped, nil
}

// names finds paths that a case-insensitive or Unicode-normalizing disk
// (macOS, Windows) would write to the same file or folder: A/x.md and
// a/y.md would end up in one folder, where a/x.md, which the commit doesn't
// have, could then be read. Every folder and file name is compared after
// case folding and NFC normalization.
type names map[string]name

type name struct {
	path string
	dir  bool
}

var fold = cases.Fold()

func (ns names) add(p string) error {
	parts := strings.Split(p, "/")
	for i := range parts {
		sub := strings.Join(parts[:i+1], "/")
		key := norm.NFC.String(fold.String(sub))
		dir := i < len(parts)-1
		if old, ok := ns[key]; ok {
			if old.path == sub && old.dir && dir {
				continue
			}
			return fmt.Errorf("%s and %s are the same name on some disks", Sanitize(old.path), Sanitize(sub))
		}
		ns[key] = name{sub, dir}
	}
	return nil
}

// CheckPath refuses paths that can't be written safely into the store:
// empty, absolute, too long or deep, . and .. components, .git,
// backslashes, control and invisible formatting characters.
func CheckPath(p string) error {
	bad := p == "" || strings.HasPrefix(p, "/") || len(p) > maxPathBytes || strings.Count(p, "/") >= maxPathDepth ||
		term.Sanitize(p) != p || strings.ContainsAny(p, "\\\n\t")
	for c := range strings.SplitSeq(p, "/") {
		bad = bad || c == "" || c == "." || c == ".." || strings.EqualFold(c, ".git")
	}
	if bad {
		return fmt.Errorf("%q is not a path ctxtra can store", Sanitize(p))
	}
	return nil
}

// Materialize writes the generation of commit unless it is in the store,
// and keeps commit with a ref so that git never deletes it. It returns the
// generation's folder. The files are written into a new private folder,
// which is renamed into place only when complete.
func (r *Repo) Materialize(ctx context.Context, commit string) (string, error) {
	dst := GenDir(r.URL, commit)
	if Has(r.URL, commit) {
		return dst, r.keep(ctx, commit)
	}
	if c, err := r.commit(ctx, commit); err != nil || c != commit {
		return "", fmt.Errorf("commit %s is not in ctxtra's copy of %s", Short(commit), r.URL)
	}
	entries, _, err := r.Tree(ctx, commit)
	if err != nil {
		return "", fmt.Errorf("%s at %s: %w", r.URL, Short(commit), err)
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		return "", err
	}
	tmp, err := os.MkdirTemp(filepath.Dir(dst), ".tmp-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(tmp)
	if err := r.writeFiles(ctx, tmp, entries); err != nil {
		return "", fmt.Errorf("%s at %s: %w", r.URL, Short(commit), err)
	}
	if err := os.Rename(tmp, dst); err != nil && !Has(r.URL, commit) {
		return "", err
	}
	return dst, r.keep(ctx, commit)
}

func (r *Repo) keep(ctx context.Context, commit string) error {
	_, err := r.git(ctx, true, nil, "update-ref", "refs/ctxtra/kept/"+commit, commit)
	return err
}

// writeFiles reads the blobs of entries with one git cat-file process and
// writes them under dir. Files are created exclusively and never through a
// symlink: dir is new and ctxtra creates every name in it.
func (r *Repo) writeFiles(ctx context.Context, dir string, entries []Entry) error {
	if len(entries) == 0 {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, readTimeout)
	defer cancel()
	var in bytes.Buffer
	for _, e := range entries {
		in.WriteString(e.OID + "\n")
	}
	// Room for every blob (Tree checked their sizes) and its header line.
	out, err := r.gitMax(ctx, true, &in, MaxTotalBytes+len(entries)*256, "cat-file", "--batch")
	if err != nil {
		return err
	}
	br := bufio.NewReader(bytes.NewReader(out))
	for _, e := range entries {
		header, err := br.ReadString('\n')
		if err != nil {
			return errors.New("short cat-file output")
		}
		f := strings.Fields(header)
		if len(f) != 3 || f[0] != e.OID || f[1] != "blob" || f[2] != strconv.FormatInt(e.Size, 10) {
			return fmt.Errorf("%s: unexpected object %q", Sanitize(e.Path), Sanitize(strings.TrimSpace(header)))
		}
		data := make([]byte, e.Size+1) // the content and a newline
		if _, err := io.ReadFull(br, data); err != nil || data[e.Size] != '\n' {
			return errors.New("short cat-file output")
		}
		if err := writeFile(dir, e, data[:e.Size]); err != nil {
			return err
		}
	}
	return nil
}

func writeFile(dir string, e Entry, data []byte) error {
	name := filepath.Join(dir, filepath.FromSlash(e.Path))
	if err := os.MkdirAll(filepath.Dir(name), 0o700); err != nil {
		return err
	}
	mode := os.FileMode(0o400)
	if e.Exec {
		mode = 0o500
	}
	f, err := os.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// Sanitize is term.Sanitize.
func Sanitize(s string) string { return term.Sanitize(s) }
