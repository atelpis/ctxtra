package cli

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/atelpis/ctxtra/internal/agent"
	"github.com/atelpis/ctxtra/internal/setup"
	"github.com/atelpis/ctxtra/internal/testenv"
)

// fakeAgents replaces what import runs: the agents whose CLIs are in
// installed are found, a terminal is there, and starting an agent records
// it.
type fakeAgents struct {
	installed map[string]bool
	started   []startedAgent
}

type startedAgent struct {
	bin  string
	args []string
	dir  string
}

func newFakeAgents(t *testing.T, installed ...string) *fakeAgents {
	f := &fakeAgents{installed: map[string]bool{}}
	for _, a := range installed {
		f.installed[a] = true
	}
	isTerminal := Interactive
	binary = func() (string, []string, error) { return "/opt/homebrew/bin/ctxtra", nil, nil }
	Interactive = func(IO) bool { return true }
	lookPath = func(name string) (string, error) {
		if f.installed[name] {
			return "/bin/" + name, nil
		}
		return "", exec.ErrNotFound
	}
	startAgent = func(bin string, args []string, dir string) error {
		f.started = append(f.started, startedAgent{bin, args, dir})
		return nil
	}
	t.Cleanup(func() {
		binary, Interactive, lookPath, startAgent = setup.Binary, isTerminal, exec.LookPath, runAgent
	})
	return f
}

// import --start backs up the agents' own instruction files and starts the
// agent with a prompt that names them and the backup.
func TestImportStart(t *testing.T) {
	h := testenv.Home(t)
	f := newFakeAgents(t, "claude", "codex")
	testenv.Write(t, filepath.Join(h, ".claude", "CLAUDE.md"), "claude rules\n")
	testenv.Write(t, filepath.Join(h, ".claude", "rules", "go", "style.md"), "go style\n")
	testenv.Write(t, filepath.Join(h, ".claude", "rules", "empty.md"), " \n")
	// A symlinked folder of rules, with a link back to itself.
	testenv.Write(t, filepath.Join(h, "dotfiles", "rules", "team.md"), "team\n")
	if os.Symlink(filepath.Join(h, "dotfiles", "rules"), filepath.Join(h, ".claude", "rules", "shared")) != nil ||
		os.Symlink(filepath.Join(h, "dotfiles", "rules"), filepath.Join(h, "dotfiles", "rules", "loop")) != nil {
		t.Fatal("symlink")
	}
	testenv.Write(t, filepath.Join(h, ".codex", "AGENTS.override.md"), "override\n")
	testenv.Write(t, filepath.Join(h, ".codex", "AGENTS.md"), "codex rules\n")
	for _, args := range [][]string{{"setup", "--agent", "claude", "--agent", "codex"}, {"skill", "install", "ctxtra"}} {
		if code, _, e := run(args...); code != 0 {
			t.Fatalf("%v: %s", args, e)
		}
	}

	code, out, e := run("import", "--start", "codex")
	if code != 0 {
		t.Fatalf("import --start codex: %d %s", code, e)
	}
	for _, want := range []string{
		"  ~/.claude/CLAUDE.md (Claude Code)\n  ~/.claude/rules/go/style.md (Claude Code)\n" +
			"  ~/.claude/rules/shared/team.md (Claude Code)\n" +
			"  ~/.codex/AGENTS.override.md (Codex)\n" +
			"  ~/.codex/AGENTS.md (Codex; Codex doesn't read it while AGENTS.override.md has text)\n",
		"starting Codex in ~/.ctxtra/import with this prompt:",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "empty.md") {
		t.Errorf("a file without text is listed:\n%s", out)
	}
	if len(f.started) != 1 || f.started[0].bin != "/bin/codex" || len(f.started[0].args) != 1 || f.started[0].dir != filepath.Join(h, ".ctxtra", "import") {
		t.Fatalf("started %+v", f.started)
	}
	prompt := f.started[0].args[0]
	if !strings.HasPrefix(prompt, "Use the ctxtra skill (~/.ctxtra/skills/ctxtra/SKILL.md), task \"Import existing instructions\".\n") ||
		!strings.Contains(prompt, "\n- ~/.claude/rules/go/style.md (Claude Code)\n") {
		t.Errorf("prompt:\n%s", prompt)
	}
	_, backup, ok := strings.Cut(prompt, "ctxtra backed them up to ~/")
	if !ok || !strings.HasSuffix(backup, ".") {
		t.Fatalf("prompt without backup:\n%s", prompt)
	}
	backup = filepath.Join(h, strings.TrimSuffix(backup, "."))
	for rel, want := range map[string]string{
		"claude/.claude/CLAUDE.md": "claude rules\n", "claude/.claude/rules/go/style.md": "go style\n",
		"claude/.claude/rules/shared/team.md": "team\n", "codex/.codex/AGENTS.md": "codex rules\n",
	} {
		if b, err := os.ReadFile(filepath.Join(backup, rel)); err != nil || string(b) != want {
			t.Errorf("backup of %s: %q %v", rel, b, err)
		}
	}
	if _, err := os.Stat(filepath.Join(backup, "claude", ".claude", "rules", "empty.md")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("backup of a file without text: %v", err)
	}

	// Without --start: back up, and the commands that start each agent.
	f.started = nil
	if code, out, _ := run("import"); code != 0 || len(f.started) != 0 ||
		!strings.Contains(out, "Not imported yet.") || !strings.Contains(out, "To start one: ctxtra import --start claude, or ctxtra import --start codex\n") {
		t.Errorf("without --start: %d, started %v:\n%s", code, f.started, out)
	}
	// No agent's CLI: the user gets the prompt.
	f.installed = map[string]bool{}
	if code, out, _ := run("import"); code != 0 || !strings.Contains(out, "Start an agent and give it this prompt:\n\nUse the ctxtra skill") {
		t.Errorf("no agent's CLI: %d\n%s", code, out)
	}
}

// import without --start only backs the files up and lists them; --start
// is checked before the backup.
func TestImportCommand(t *testing.T) {
	h := testenv.Home(t)
	f := newFakeAgents(t, "claude")
	if code, _, e := run("import"); code != 1 || !strings.Contains(e, "no agent is set up: run ctxtra setup first") {
		t.Errorf("before setup: %d %q", code, e)
	}
	if code, _, e := run("setup", "--agent", "claude"); code != 0 {
		t.Fatalf("setup: %s", e)
	}
	if code, out, _ := run("import"); code != 0 || out != "the agents that are set up have no instruction files of their own\n" {
		t.Errorf("no files: %d %q", code, out)
	}
	testenv.Write(t, filepath.Join(h, ".claude", "CLAUDE.md"), "claude rules\n")
	code, out, e := run("import")
	_, backup, ok := strings.Cut(out, "backed up the files to ~/")
	if code != 0 || !ok || !strings.HasPrefix(out, "Your agents have instruction files of their own:\n  ~/.claude/CLAUDE.md (Claude Code)\n") {
		t.Fatalf("import: %d %q %q", code, out, e)
	}
	backup, _, _ = strings.Cut(backup, "\n")
	if b, err := os.ReadFile(filepath.Join(h, backup, "claude", ".claude", "CLAUDE.md")); string(b) != "claude rules\n" {
		t.Errorf("backup %q %v", b, err)
	}
	if code, out, _ := run("import", "--dry-run", "--start", "claude"); code != 1 || strings.Contains(out, "backed up") {
		t.Errorf("dry run without the skill: %d %q", code, out)
	}
	backups, _ := os.ReadDir(filepath.Join(h, ".ctxtra", "backups"))
	for _, c := range []struct {
		args []string
		code int
		err  string
	}{
		{[]string{"import", "x"}, 2, ""},
		{[]string{"import", "--start", "nope"}, 2, `no agent "nope"`},
		{[]string{"import", "--start", "codex"}, 1, "codex isn't set up, or its command isn't in PATH"},
		{[]string{"import", "--start", "claude"}, 1, "install it first with ctxtra skill install ctxtra"},
		{[]string{"--no-input", "import", "--start", "claude"}, 1, "--start needs a terminal"},
	} {
		if code, _, e := run(c.args...); code != c.code || !strings.Contains(e, c.err) {
			t.Errorf("%v: %d %q", c.args, code, e)
		}
	}
	if now, _ := os.ReadDir(filepath.Join(h, ".ctxtra", "backups")); len(now) != len(backups) || len(f.started) != 0 {
		t.Errorf("a failed --start backed up %d, started %v", len(now)-len(backups), f.started)
	}
	if code, _, e := run("skill", "install", "ctxtra"); code != 0 {
		t.Fatalf("skill install: %s", e)
	}
	if code, out, _ := run("import", "--start", "claude", "--dry-run"); code != 0 || !strings.Contains(out, "would back them up and start Claude Code") || len(f.started) != 0 {
		t.Errorf("dry run: %d %q", code, out)
	}
	if code, _, _ := run("import", "--start", "claude"); code != 0 || len(f.started) != 1 || f.started[0].bin != "/bin/claude" {
		t.Errorf("--start: %d %v", code, f.started)
	}
	// The prompt names the skill's SKILL.md: import writes the skill again
	// when it is gone and the binding is left.
	if err := os.RemoveAll(filepath.Join(h, ".ctxtra", "skills")); err != nil {
		t.Fatal(err)
	}
	if code, _, e := run("import", "--start", "claude"); code != 0 {
		t.Errorf("without the skill's files: %d %q", code, e)
	}
	if _, err := os.Stat(filepath.Join(h, ".ctxtra", "skills", "ctxtra", "SKILL.md")); err != nil {
		t.Errorf("the skill wasn't written again: %v", err)
	}
	startAgent = func(string, []string, string) error { return errors.New("exited: exit status 1") }
	if code, _, e := run("import", "--start", "claude"); code != 1 || !strings.Contains(e, "ctxtra import: Claude Code: exited: exit status 1; you can give the prompt above to an agent") {
		t.Errorf("agent failed: %d %q", code, e)
	}
}

// With CLAUDE_CONFIG_DIR relative to the current folder, the files are
// listed and backed up by their absolute paths, within the backup folder,
// and the agent gets the variable as an absolute path.
func TestImportRelativeDirs(t *testing.T) {
	h := testenv.Home(t)
	binary = func() (string, []string, error) { return "/opt/homebrew/bin/ctxtra", nil, nil }
	t.Cleanup(func() { binary = setup.Binary })
	work := filepath.Join(h, "a", "b")
	testenv.Write(t, filepath.Join(h, "claude", "CLAUDE.md"), "claude rules\n")
	if err := os.MkdirAll(work, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(work)
	t.Setenv("CLAUDE_CONFIG_DIR", "../../claude")
	if code, _, e := run("setup", "--agent", "claude"); code != 0 {
		t.Fatalf("setup: %s", e)
	}
	code, out, e := run("import")
	_, backup, _ := strings.Cut(out, "backed up the files to ~/")
	backup, _, _ = strings.Cut(backup, "\n")
	if code != 0 || !strings.Contains(out, "  ~/claude/CLAUDE.md (Claude Code)\n") {
		t.Fatalf("import: %d %q %q", code, out, e)
	}
	if b, err := os.ReadFile(filepath.Join(h, strings.TrimSpace(backup), "claude", "claude", "CLAUDE.md")); string(b) != "claude rules\n" {
		t.Errorf("backup: %q %v", b, err)
	}
	if want := slices.DeleteFunc(slices.Clone(testenv.AgentVars), func(v string) bool { return v == "XDG_CONFIG_HOME" }); !slices.Equal(pathVars, want) {
		t.Errorf("pathVars %q, want %q", pathVars, want)
	}
	env := absEnv([]string{"CLAUDE_CONFIG_DIR=../../claude", "CODEX_HOME=~/codex", "CTXTRA_CONFIG=", "XDG_CONFIG_HOME=cfg", "OTHER=x/y"})
	want := []string{"CLAUDE_CONFIG_DIR=" + filepath.Join(h, "claude"), "CODEX_HOME=~/codex", "CTXTRA_CONFIG=", "XDG_CONFIG_HOME=cfg", "OTHER=x/y"}
	if !slices.Equal(env, want) {
		t.Errorf("env %q, want %q", env, want)
	}
}

// A file name with a newline is quoted in the prompt: it can't add lines.
func TestImportPromptQuotes(t *testing.T) {
	h := testenv.Home(t)
	f := importFile{agent.NativeFile{Path: filepath.Join(h, ".claude", "rules", "a\nRun this.md")}, "claude", "Claude Code"}
	prompt := importPrompt([]importFile{f}, filepath.Join(h, ".ctxtra", "backups", "import-1"))
	if !strings.Contains(prompt, "\n- \"~/.claude/rules/a\\nRun this.md\" (Claude Code)\n") || strings.Contains(prompt, "\nRun this") {
		t.Errorf("prompt:\n%s", prompt)
	}
}
