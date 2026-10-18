// Package testenv sets up an isolated home folder, config and git repositories
// for tests. Tests never touch the real user's files.
package testenv

import (
	"net/http/cgi"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Home gives the test its own HOME and clears every variable that could
// point at the real user's files. It returns the home folder.
func Home(t *testing.T) string {
	t.Helper()
	h, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", h)
	for _, k := range []string{"CTXTRA_CONFIG", "CTXTRA_LOG", "CLAUDE_CONFIG_DIR", "CODEX_HOME", "PI_CODING_AGENT_DIR", "OPENCODE_CONFIG_DIR", "XDG_CONFIG_HOME", "HERMES_HOME",
		"DSH_HOME", "DEVIN_PROJECT_DIR"} {
		t.Setenv(k, "")
	}
	// Git variables that point git at another repository, index or config:
	// git sets them when it runs a hook, so a test run from a pre-commit hook
	// would otherwise commit into the real repository.
	for _, k := range GitLocationVars {
		t.Setenv(k, "") // restores the old value after the test
		os.Unsetenv(k)
	}
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("GIT_AUTHOR_NAME", "t")
	t.Setenv("GIT_AUTHOR_EMAIL", "t@example.com")
	t.Setenv("GIT_COMMITTER_NAME", "t")
	t.Setenv("GIT_COMMITTER_EMAIL", "t@example.com")
	return h
}

// GitLocationVars are cleared by Home. gitrepo clears the same list for
// production git calls (a test checks that the lists match).
var GitLocationVars = []string{
	"GIT_DIR", "GIT_WORK_TREE", "GIT_COMMON_DIR", "GIT_INDEX_FILE", "GIT_OBJECT_DIRECTORY",
	"GIT_ALTERNATE_OBJECT_DIRECTORIES", "GIT_NAMESPACE", "GIT_PREFIX", "GIT_CEILING_DIRECTORIES",
	"GIT_DISCOVERY_ACROSS_FILESYSTEM", "GIT_CONFIG", "GIT_CONFIG_PARAMETERS", "GIT_CONFIG_COUNT",
}

// Write creates path with content, and its parent folders.
func Write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// Run runs a command in dir and fails the test on error.
func Run(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command(args[0], args[1:]...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%v: %v\n%s", args, err, out)
	}
}

// Repo creates a git repository in dir with one commit and, if remote is
// set, an origin remote. It returns dir.
func Repo(t *testing.T, dir, remote string) string {
	t.Helper()
	Write(t, filepath.Join(dir, "AGENTS.md"), "team\n")
	Run(t, dir, "git", "init", "-q", "-b", "main")
	Run(t, dir, "git", "add", ".")
	Run(t, dir, "git", "commit", "-q", "-m", "init")
	if remote != "" {
		Run(t, dir, "git", "remote", "add", "origin", remote)
	}
	return dir
}

// Config writes the config file and returns its path.
func Config(t *testing.T, home, yaml string) string {
	t.Helper()
	p := filepath.Join(home, ".config", "ctxtra", "config.yaml")
	Write(t, p, yaml)
	return p
}

// Skill creates a skill folder with a SKILL.md that has the given frontmatter.
func Skill(t *testing.T, dir, front string) {
	t.Helper()
	Write(t, filepath.Join(dir, "SKILL.md"), "---\n"+front+"\n---\n\nBody of "+filepath.Base(dir)+".\n")
}

// GitServer serves the git repositories in root over https with git
// http-backend, so that tests can fetch with the transports ctxtra allows. A
// repository root/x is at <url>/x. Git doesn't verify the test certificate:
// GitServer sets GIT_SSL_NO_VERIFY for the test.
func GitServer(t *testing.T, root string) string {
	t.Helper()
	url, stop, err := StartGitServer(root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(stop)
	t.Setenv("GIT_SSL_NO_VERIFY", "1")
	return url
}

// StartGitServer is GitServer for callers without a *testing.T. The caller
// sets GIT_SSL_NO_VERIFY=1 for git.
func StartGitServer(root string) (url string, stop func(), err error) {
	out, err := exec.Command("git", "--exec-path").Output()
	if err != nil {
		return "", nil, err
	}
	backend := filepath.Join(strings.TrimSpace(string(out)), "git-http-backend")
	h := &cgi.Handler{
		Path: backend,
		Env:  []string{"GIT_PROJECT_ROOT=" + root, "GIT_HTTP_EXPORT_ALL=1", "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=" + os.DevNull},
	}
	srv := httptest.NewTLSServer(h)
	return srv.URL, srv.Close, nil
}
