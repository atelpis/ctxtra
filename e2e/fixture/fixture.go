// Package fixture builds an isolated home folder for one agent run: git
// repositories and worktrees, instruction files with CTXMARK markers, skills,
// and the ctxtra config, and runs ctxtra in it.
package fixture

import (
	"bufio"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Fixture is a fake home folder. Every path in it is real (no symlinks), so
// it compares equal to what git and the agents report.
type Fixture struct {
	t    testing.TB
	Home string
	Bin  string // the ctxtra binary under test
}

// New creates an empty home for the test, in dir, or in a temporary folder
// removed after the test if dir is "".
func New(t testing.TB, bin, dir string) *Fixture {
	t.Helper()
	if dir == "" {
		dir = t.TempDir()
	}
	home, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	f := &Fixture{t: t, Home: home, Bin: bin}
	f.mkdir("tmp")
	return f
}

// Path is rel inside the home folder.
func (f *Fixture) Path(rel string) string { return filepath.Join(f.Home, rel) }

func (f *Fixture) mkdir(rel string) {
	if err := os.MkdirAll(f.Path(rel), 0o755); err != nil {
		f.t.Fatal(err)
	}
}

// Write creates the file rel with content.
func (f *Fixture) Write(rel, content string) string {
	f.t.Helper()
	p := f.Path(rel)
	f.mkdir(filepath.Dir(rel))
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		f.t.Fatal(err)
	}
	return p
}

// Marked creates the file rel that contains only the marker CTXMARK-name.
func (f *Fixture) Marked(rel, name string) string { return f.Write(rel, "CTXMARK-"+name+"\n") }

// Skill creates a skill folder rel whose description holds CTXMARK-desc and
// whose SKILL.md body holds CTXMARK-body.
func (f *Fixture) Skill(rel, name, desc, body string) string {
	f.Write(filepath.Join(rel, "SKILL.md"), "---\nname: "+name+"\ndescription: Use for "+name+
		" tasks. CTXMARK-"+desc+"\n---\n\n# "+name+"\n\nCTXMARK-"+body+"\n")
	return f.Path(rel)
}

// Repo creates a git repository at rel with files (path -> marker name)
// committed, and an origin remote if remote isn't empty.
func (f *Fixture) Repo(rel, remote string, files map[string]string) string {
	f.t.Helper()
	f.mkdir(rel)
	for p, m := range files {
		f.Marked(filepath.Join(rel, p), m)
	}
	f.Git(rel, "init", "-q", "-b", "main")
	f.Git(rel, "add", "-A")
	f.Git(rel, "commit", "-q", "--allow-empty", "-m", "init")
	if remote != "" {
		f.Git(rel, "remote", "add", "origin", remote)
	}
	return f.Path(rel)
}

// Worktree adds a worktree of the repository at repo, on a new branch, at
// rel, which may be outside the repository's folder.
func (f *Fixture) Worktree(repo, rel, branch string) string {
	f.Git(repo, "worktree", "add", "-q", "-b", branch, f.Path(rel))
	return f.Path(rel)
}

// Git runs git in rel and returns its output.
func (f *Fixture) Git(rel string, args ...string) string {
	f.t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = f.Path(rel)
	cmd.Env = f.Env()
	out, err := cmd.CombinedOutput()
	if err != nil {
		f.t.Fatalf("git %s in %s: %v\n%s", strings.Join(args, " "), rel, err, out)
	}
	return string(out)
}

// Clean fails the test if a checkout has changes: ctxtra must never write
// into repositories.
func (f *Fixture) Clean(rels ...string) {
	f.t.Helper()
	for _, rel := range rels {
		if out := f.Git(rel, "status", "--porcelain"); out != "" {
			f.t.Errorf("%s has changes:\n%s", rel, out)
		}
	}
}

// Config writes the ctxtra config, which lists one binding file with
// the given YAML documents.
func (f *Fixture) Config(bindings string) {
	f.Write(".config/ctxtra/config.yaml", "apiVersion: ctxtra/v1alpha1\nkind: Config\nbindings: [bindings.yaml]\n")
	f.Write(".config/ctxtra/bindings.yaml", bindings)
}

// Env is the minimal environment for every process in the fixture: no
// variable from the user's shell except PATH, git that reads no user or
// system config, and the agents' config folders inside the home.
func (f *Fixture) Env() []string {
	return []string{
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + f.Home,
		"TMPDIR=" + f.Path("tmp"),
		"LANG=en_US.UTF-8",
		"TERM=dumb",
		"GIT_CONFIG_GLOBAL=" + os.DevNull,
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_AUTHOR_NAME=e2e", "GIT_AUTHOR_EMAIL=e2e@example.invalid",
		"GIT_COMMITTER_NAME=e2e", "GIT_COMMITTER_EMAIL=e2e@example.invalid",
		"CLAUDE_CONFIG_DIR=" + f.Path(".claude"),
		"CODEX_HOME=" + f.Path(".codex"),
		"PI_CODING_AGENT_DIR=" + f.Path(".pi/agent"),
		"COPILOT_HOME=" + f.Path(".copilot"),
		"DSH_HOME=" + f.Path(".dsh"),
	}
}

// Ctxtra runs the ctxtra binary and fails the test on an error.
func (f *Fixture) Ctxtra(args ...string) string {
	f.t.Helper()
	cmd := exec.Command(f.Bin, args...)
	cmd.Dir = f.Home
	cmd.Env = f.Env()
	out, err := cmd.CombinedOutput()
	if err != nil {
		f.t.Fatalf("ctxtra %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return string(out)
}

// LogEntry is one line of ctxtra's hook log.
type LogEntry struct {
	Harness, Event, Dir, Repo, Source, Error string
	AgentType                                string `json:"agent_type"`
	Files, Skills, Missing                   []string
}

// Log is ctxtra's hook log in the home folder.
func (f *Fixture) Log() []LogEntry {
	fh, err := os.Open(f.Path(".ctxtra/log.jsonl"))
	if err != nil {
		return nil
	}
	defer fh.Close()
	var out []LogEntry
	sc := bufio.NewScanner(fh)
	sc.Buffer(nil, 1<<20)
	for sc.Scan() {
		var e LogEntry
		if json.Unmarshal(sc.Bytes(), &e) == nil {
			out = append(out, e)
		}
	}
	return out
}
