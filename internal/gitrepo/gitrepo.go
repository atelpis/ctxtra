// Package gitrepo identifies the git repository a session runs in.
package gitrepo

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/atelpis/ctxtra/internal/paths"
)

// timeout is the time all git calls for one session may take together. It is
// well under the agents' hook timeout (10 seconds), so a slow repository
// costs the repos bindings, not the whole hook. A variable for tests.
var timeout = 3 * time.Second

// locationVars point git at another repository, work tree, index or config
// instead of the folder given with -C. An agent can inherit them (git sets
// them when it runs hooks), so they are removed for every git call.
var locationVars = []string{
	"GIT_DIR", "GIT_WORK_TREE", "GIT_COMMON_DIR", "GIT_INDEX_FILE", "GIT_OBJECT_DIRECTORY",
	"GIT_ALTERNATE_OBJECT_DIRECTORIES", "GIT_NAMESPACE", "GIT_PREFIX", "GIT_CEILING_DIRECTORIES",
	"GIT_DISCOVERY_ACROSS_FILESYSTEM", "GIT_CONFIG", "GIT_CONFIG_PARAMETERS", "GIT_CONFIG_COUNT",
}

// Repo is the git repository of a session directory. Root is the same for the
// main checkout and every worktree, wherever the worktree is on disk.
type Repo struct {
	Root    string   // main checkout; the git directory for a bare or --separate-git-dir repository
	Top     string   // top of the worktree the session runs in
	Subpath string   // session directory relative to Top; "." at the top
	Remotes []string // normalized, sorted, unique
}

// Identify returns nil when dir is not inside a git repository, or when git
// doesn't answer in time: then the session counts as outside git.
func Identify(dir string) *Repo {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	// One call for both values. --show-toplevel fails in a bare repository;
	// then ask for the git directory alone.
	out, ok := git(ctx, dir, "rev-parse", "--path-format=absolute", "--git-common-dir", "--show-toplevel")
	lines := strings.Split(out, "\n")
	if !ok {
		out, ok = git(ctx, dir, "rev-parse", "--path-format=absolute", "--git-common-dir")
		lines = []string{out}
	}
	if !ok || lines[0] == "" {
		return nil
	}
	common := paths.Real(lines[0])
	r := &Repo{Root: common, Subpath: "."}
	if len(lines) > 1 && lines[1] != "" {
		r.Top = paths.Real(lines[1])
		if rel, err := filepath.Rel(r.Top, paths.Real(dir)); err == nil && rel != ".." && !strings.HasPrefix(rel, "../") {
			r.Subpath = filepath.ToSlash(rel)
		}
	}
	switch {
	case filepath.Base(common) == ".git":
		r.Root = filepath.Dir(common)
	case r.Top != "":
		// A submodule: its git directory is inside the superproject's. For
		// a --separate-git-dir checkout git doesn't record the main
		// checkout, so Root stays the git directory, the same for every
		// worktree.
		if main := submoduleCheckout(ctx, dir, common); main != "" {
			r.Root = main
		}
	}
	out, _ = git(ctx, dir, "remote", "-v")
	for line := range strings.SplitSeq(out, "\n") {
		if f := strings.Fields(line); len(f) >= 2 {
			r.Remotes = append(r.Remotes, NormalizeRemote(f[1]))
		}
	}
	slices.Sort(r.Remotes)
	r.Remotes = slices.Compact(r.Remotes)
	if ctx.Err() != nil {
		return nil // a partial answer could match the wrong bindings
	}
	return r
}

// submoduleCheckout is the checkout of a submodule, which git records in
// core.worktree relative to the git directory common. It is "" for other
// layouts.
func submoduleCheckout(ctx context.Context, dir, common string) string {
	wt, ok := git(ctx, dir, "config", "--get", "core.worktree")
	if !ok || wt == "" {
		return ""
	}
	if !filepath.IsAbs(wt) {
		wt = filepath.Join(common, wt)
	}
	return paths.Real(wt)
}

var (
	reScheme = regexp.MustCompile(`^[a-z+]+://`)
	reUser   = regexp.MustCompile(`^[^@/]+@`)
	rePort   = regexp.MustCompile(`^([^/:]+):(\d+)/`)
	reColon  = regexp.MustCompile(`^([^/:]+):`)
	reDotGit = regexp.MustCompile(`\.git/?$`)
)

// NormalizeRemote turns git@github.com:Org/Repo.git, https://github.com/org/repo
// and ssh://git@host:22/org/repo into host/org/repo, in lower case.
func NormalizeRemote(url string) string {
	u := strings.TrimSpace(url)
	u = reScheme.ReplaceAllString(u, "")
	u = reUser.ReplaceAllString(u, "")
	u = rePort.ReplaceAllString(u, "$1/")
	u = reColon.ReplaceAllString(u, "$1/")
	u = reDotGit.ReplaceAllString(u, "")
	return strings.ToLower(strings.TrimRight(u, "/"))
}

// CleanEnv is the environment without the variables that point git at
// another repository or config.
func CleanEnv() []string {
	return slices.DeleteFunc(os.Environ(), func(kv string) bool {
		k, _, _ := strings.Cut(kv, "=")
		return slices.Contains(locationVars, k)
	})
}

func git(ctx context.Context, dir string, args ...string) (string, bool) {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
	cmd.Env = CleanEnv()
	// Don't wait for a child process that keeps stdout open after git is
	// killed.
	cmd.WaitDelay = 500 * time.Millisecond
	out, err := cmd.Output()
	if err != nil {
		return "", false
	}
	return strings.TrimSpace(string(out)), true
}
