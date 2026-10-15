package gitrepo

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/atelpis/ctxtra/internal/testenv"
)

func TestNormalizeRemote(t *testing.T) {
	for in, want := range map[string]string{
		"git@github.com:Acme/Svc.git":         "github.com/acme/svc",
		"https://github.com/acme/svc":         "github.com/acme/svc",
		"https://user@github.com/acme/svc/":   "github.com/acme/svc",
		"ssh://git@host.example:2222/a/b.git": "host.example/a/b",
		"git@gitlab.com:group/sub/repo.git":   "gitlab.com/group/sub/repo",
	} {
		if got := NormalizeRemote(in); got != want {
			t.Errorf("NormalizeRemote(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestIdentify(t *testing.T) {
	h := testenv.Home(t)
	r := testenv.Repo(t, filepath.Join(h, "dev", "svc"), "git@github.com:Acme/Svc.git")
	wt := filepath.Join(h, "elsewhere", "wt")
	testenv.Run(t, r, "git", "worktree", "add", "-q", wt, "-b", "feature")
	sub := filepath.Join(wt, "services", "..billing")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}

	got := Identify(sub)
	if got == nil {
		t.Fatal("not identified")
	}
	if got.Root != r || got.Top != wt || got.Subpath != "services/..billing" {
		t.Errorf("got %+v", got)
	}
	if len(got.Remotes) != 1 || got.Remotes[0] != "github.com/acme/svc" {
		t.Errorf("remotes %v", got.Remotes)
	}
	if Identify(h) != nil {
		t.Error("home is not a repository")
	}
}

// git doesn't record where a --separate-git-dir checkout is, so Root is the
// git directory: still the same for the checkout and its worktrees.
func TestIdentifySeparateGitDir(t *testing.T) {
	h := testenv.Home(t)
	main := filepath.Join(h, "main")
	if err := os.Mkdir(filepath.Join(h, "gitdirs"), 0o755); err != nil {
		t.Fatal(err)
	}
	testenv.Run(t, h, "git", "init", "-q", "-b", "main", "--separate-git-dir", filepath.Join(h, "gitdirs", "main"), main)
	testenv.Write(t, filepath.Join(main, "f"), "x")
	testenv.Run(t, main, "git", "add", ".")
	testenv.Run(t, main, "git", "commit", "-q", "-m", "init")
	wt := filepath.Join(h, "wt")
	testenv.Run(t, main, "git", "worktree", "add", "-q", wt, "-b", "feature")
	for _, dir := range []string{main, wt} {
		if got := Identify(dir); got == nil || got.Root != filepath.Join(h, "gitdirs", "main") || got.Top != dir {
			t.Errorf("%s: %+v", dir, got)
		}
	}
}

func TestIdentifySubmodule(t *testing.T) {
	h := testenv.Home(t)
	lib := testenv.Repo(t, filepath.Join(h, "lib"), "")
	app := testenv.Repo(t, filepath.Join(h, "app"), "")
	testenv.Run(t, app, "git", "-c", "protocol.file.allow=always", "submodule", "add", "-q", lib, "vendor/lib")
	sub := filepath.Join(app, "vendor", "lib")
	if got := Identify(sub); got == nil || got.Root != sub || got.Top != sub {
		t.Errorf("submodule: %+v", got)
	}
}

// An agent started from a git hook has GIT_DIR set. The session folder still
// decides which repository it is.
func TestIdentifyIgnoresGitEnv(t *testing.T) {
	if !slices.Equal(locationVars, testenv.GitLocationVars) {
		t.Fatal("locationVars and testenv.GitLocationVars differ")
	}
	h := testenv.Home(t)
	other := testenv.Repo(t, filepath.Join(h, "other"), "git@github.com:acme/other.git")
	svc := testenv.Repo(t, filepath.Join(h, "svc"), "git@github.com:acme/svc.git")
	plain := filepath.Join(h, "plain")
	if err := os.Mkdir(plain, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_DIR", filepath.Join(other, ".git"))
	t.Setenv("GIT_WORK_TREE", other)
	if got := Identify(plain); got != nil {
		t.Errorf("plain folder: %+v", got)
	}
	if got := Identify(svc); got == nil || got.Root != svc || !slices.Equal(got.Remotes, []string{"github.com/acme/svc"}) {
		t.Errorf("svc: %+v", got)
	}
}

// When git answers rev-parse but then hangs, the session counts as outside
// git: a partial answer could still match by the main checkout.
func TestIdentifyTimeout(t *testing.T) {
	h := testenv.Home(t)
	r := testenv.Repo(t, filepath.Join(h, "svc"), "git@github.com:acme/svc.git")
	realGit, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	testenv.Write(t, filepath.Join(bin, "git"), "#!/bin/sh\ncase \"$*\" in *remote*) exec sleep 10;; esac\nexec "+realGit+" \"$@\"\n")
	if err := os.Chmod(filepath.Join(bin, "git"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+":/usr/bin:/bin")
	old := timeout
	timeout = 500 * time.Millisecond
	t.Cleanup(func() { timeout = old })
	start := time.Now()
	if got := Identify(r); got != nil {
		t.Errorf("partial answer used: %+v", got)
	}
	if d := time.Since(start); d > 3*time.Second {
		t.Errorf("took %v", d)
	}
}
