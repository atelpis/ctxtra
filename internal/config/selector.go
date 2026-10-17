package config

import (
	"fmt"
	"path"
	"path/filepath"
	"strings"

	"github.com/bmatcuk/doublestar/v4"
	"go.yaml.in/yaml/v3"

	"github.com/atelpis/ctxtra/internal/gitrepo"
	"github.com/atelpis/ctxtra/internal/paths"
)

// Selector is a binding's match block: which sessions the binding applies
// to. It matches when any of its entries matches.
type Selector struct {
	Global   bool      // every session
	Dirs     []string  // globs on the session folder or the repository's main checkout
	GitRepos []GitRepo // repos: repositories by remote
}

// GitRepo matches a repository by remote and, optionally, the part of the
// repository the session started in. Both must match.
type GitRepo struct {
	Remote  string // glob on a normalized remote (host/owner/repo)
	Subpath string // glob on the session folder relative to the worktree top
}

func decodeSelector(n *yaml.Node) (Selector, error) {
	f, err := fields(n, "global", "dirs", "repos")
	if err != nil {
		return Selector{}, err
	}
	if len(f) == 0 {
		return Selector{}, fmt.Errorf("line %d: match is empty; use `global: true` for every session", n.Line)
	}
	var s Selector
	if v := f["global"]; v != nil {
		if v.ShortTag() != "!!bool" || v.Value != "true" {
			return Selector{}, fmt.Errorf("line %d: global must be true (remove it otherwise)", v.Line)
		}
		s.Global = true
	}
	if v := f["dirs"]; v != nil {
		dirs, err := strList(v, "dirs")
		if err != nil {
			return Selector{}, err
		}
		for _, d := range dirs {
			d = paths.Expand(d)
			if !filepath.IsAbs(d) {
				return Selector{}, fmt.Errorf("line %d: dirs entry %q must be absolute or start with ~", v.Line, d)
			}
			if !doublestar.ValidatePattern(filepath.ToSlash(d)) {
				return Selector{}, fmt.Errorf("line %d: dirs entry %q is not a valid pattern", v.Line, d)
			}
			s.Dirs = append(s.Dirs, filepath.ToSlash(filepath.Clean(d)))
		}
	}
	if v := f["repos"]; v != nil {
		if v.Kind != yaml.SequenceNode || len(v.Content) == 0 {
			return Selector{}, fmt.Errorf("line %d: repos must be a non-empty list", v.Line)
		}
		for _, e := range v.Content {
			g, err := decodeGitRepo(e)
			if err != nil {
				return Selector{}, err
			}
			s.GitRepos = append(s.GitRepos, g)
		}
	}
	return s, nil
}

// decodeGitRepo reads a repos entry: a remote pattern, or a map with remote
// and subpath.
func decodeGitRepo(n *yaml.Node) (GitRepo, error) {
	var g GitRepo
	var err error
	if n.Kind == yaml.ScalarNode {
		if g.Remote, err = str(n, "repos entry"); err != nil {
			return GitRepo{}, err
		}
		return g, checkRemote(n, &g)
	}
	f, err := fields(n, "remote", "subpath")
	if err != nil {
		return GitRepo{}, err
	}
	if err := require(n, f, "remote"); err != nil {
		return GitRepo{}, err
	}
	if g.Remote, err = str(f["remote"], "remote"); err != nil {
		return GitRepo{}, err
	}
	if err := checkRemote(n, &g); err != nil {
		return GitRepo{}, err
	}
	if v := f["subpath"]; v != nil {
		if g.Subpath, err = str(v, "subpath"); err != nil {
			return GitRepo{}, err
		}
		g.Subpath = path.Clean(strings.TrimPrefix(g.Subpath, "./"))
		if path.IsAbs(g.Subpath) || g.Subpath == ".." || strings.HasPrefix(g.Subpath, "../") {
			return GitRepo{}, fmt.Errorf("line %d: subpath %q must be relative to the repository top", v.Line, g.Subpath)
		}
		if !doublestar.ValidatePattern(g.Subpath) {
			return GitRepo{}, fmt.Errorf("line %d: subpath %q is not a valid pattern", v.Line, g.Subpath)
		}
	}
	return g, nil
}

// checkRemote lower-cases the remote pattern of g and checks it.
func checkRemote(n *yaml.Node, g *GitRepo) error {
	g.Remote = strings.ToLower(g.Remote)
	if !doublestar.ValidatePattern(g.Remote) {
		return fmt.Errorf("line %d: remote %q is not a valid pattern", n.Line, g.Remote)
	}
	return nil
}

// Match reports whether s applies to a session in dir (repo is nil outside
// git), and why.
func (s Selector) Match(dir string, repo *gitrepo.Repo) (bool, string) {
	if s.Global {
		return true, "global"
	}
	candidates := []string{filepath.ToSlash(dir)}
	if repo != nil && repo.Root != dir {
		candidates = append(candidates, filepath.ToSlash(repo.Root))
	}
	for _, pat := range s.Dirs {
		for _, c := range candidates {
			if globMatch(pat, c) || globMatch(realPattern(pat), c) {
				return true, fmt.Sprintf("dirs %s matches %s", pat, paths.Tilde(c))
			}
		}
	}
	for _, g := range s.GitRepos {
		if repo == nil {
			break
		}
		for _, r := range repo.Remotes {
			if !globMatch(g.Remote, r) {
				continue
			}
			if g.Subpath == "" {
				return true, "repos remote " + r
			}
			if globMatch(g.Subpath, repo.Subpath) {
				return true, fmt.Sprintf("repos remote %s, subpath %s", r, repo.Subpath)
			}
		}
	}
	var why []string
	if len(s.Dirs) > 0 {
		why = append(why, "dirs do not match "+paths.Tilde(dir))
	}
	if len(s.GitRepos) > 0 {
		switch {
		case repo == nil:
			why = append(why, "repos: not in a git repository")
		case len(repo.Remotes) == 0:
			why = append(why, "repos: the repository has no remotes")
		default:
			why = append(why, fmt.Sprintf("repos do not match %s (subpath %s)", strings.Join(repo.Remotes, ", "), repo.Subpath))
		}
	}
	return false, strings.Join(why, "; ")
}

// realPattern resolves symlinks in the fixed start of a pattern, so that
// /tmp/** also matches /private/tmp/x on macOS. A pattern without glob
// characters is a plain path and is resolved whole: /tmp itself is a symlink.
func realPattern(pat string) string {
	if !strings.ContainsAny(pat, `*?[{\`) {
		return filepath.ToSlash(paths.Real(filepath.FromSlash(pat)))
	}
	base, rest := doublestar.SplitPattern(pat)
	real := strings.TrimSuffix(filepath.ToSlash(paths.Real(filepath.FromSlash(base))), "/")
	if rest == "" || rest == "." {
		return real
	}
	return real + "/" + rest
}

func globMatch(pattern, name string) bool {
	ok, err := doublestar.Match(pattern, name)
	return err == nil && ok
}

// String describes s in one line, for add and sync.
func (s Selector) String() string {
	if s.Global {
		return "every session"
	}
	var parts []string
	for _, d := range s.Dirs {
		parts = append(parts, paths.Tilde(filepath.FromSlash(d)))
	}
	for _, g := range s.GitRepos {
		r := g.Remote
		if g.Subpath != "" {
			r += " (subpath " + g.Subpath + ")"
		}
		parts = append(parts, r)
	}
	return strings.Join(parts, ", ")
}
