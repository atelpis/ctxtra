package cli

import (
	"bytes"
	"errors"
	"io"
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

// In a terminal, with the skill installed and no instructions bound, setup
// offers to start an agent that moves the agents' own instruction files into
// ctxtra: it backs them up and lists them in the prompt.
func TestSetupOffersImport(t *testing.T) {
	h := testenv.Home(t)
	isTerminal := interactive
	binary = func() (string, []string, error) { return "/opt/homebrew/bin/ctxtra", nil, nil }
	interactive = func(io.Reader) bool { return true }
	installed := map[string]bool{"claude": true, "codex": true}
	lookPath = func(name string) (string, error) {
		if installed[name] {
			return "/bin/" + name, nil
		}
		return "", exec.ErrNotFound
	}
	type start struct {
		bin  string
		args []string
		dir  string
	}
	var started []start
	startAgent = func(bin string, args []string, dir string) error {
		started = append(started, start{bin, args, dir})
		return nil
	}
	t.Cleanup(func() {
		binary, interactive, lookPath, startAgent = setup.Binary, isTerminal, exec.LookPath, runAgent
	})
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
	setupWith := func(answer string, agents ...string) string {
		t.Helper()
		started = nil
		if len(agents) == 0 {
			agents = []string{"claude", "codex"}
		}
		args := []string{"setup"}
		for _, a := range agents {
			args = append(args, "--agent", a)
		}
		var o, e bytes.Buffer
		code := Run(args, IO{In: strings.NewReader(answer), Out: &o, Err: &e}, "test")
		if code != 0 {
			t.Fatalf("setup: %d %s", code, e.String())
		}
		return o.String()
	}

	// Enter installs the skill, Enter accepts, 2 chooses Codex.
	out := setupWith("\n\n2\n")
	for _, want := range []string{
		"  ~/.claude/CLAUDE.md (Claude Code)\n  ~/.claude/rules/go/style.md (Claude Code)\n" +
			"  ~/.claude/rules/shared/team.md (Claude Code)\n" +
			"  ~/.codex/AGENTS.override.md (Codex)\n" +
			"  ~/.codex/AGENTS.md (Codex; Codex doesn't read it while AGENTS.override.md has text)\n",
		"Do this now? [Y/n] Which agent?\n  1) Claude Code\n  2) Codex\nChoose 1-2 [1]: ",
		"starting Codex in ~/.ctxtra/import with this prompt:",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "empty.md") {
		t.Errorf("a file without text is listed:\n%s", out)
	}
	if len(started) != 1 || started[0].bin != "/bin/codex" || len(started[0].args) != 1 || started[0].dir != filepath.Join(h, ".ctxtra", "import") {
		t.Fatalf("started %+v", started)
	}
	prompt := started[0].args[0]
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

	// The skill is installed now: the first answer is for the import.
	for _, answer := range []string{"n\n", "", "\n3\n", "\nx\n"} {
		if out := setupWith(answer); !strings.Contains(out, "not started; to do it later") || len(started) != 0 {
			t.Errorf("declined with %q, started %v:\n%s", answer, started, out)
		}
	}
	if setupWith("\n\n"); len(started) != 1 || started[0].bin != "/bin/claude" {
		t.Errorf("Enter chose %+v", started)
	}

	// Setup of one agent: the agents of earlier runs are offered too.
	if out := setupWith("\n2\n", "claude"); !strings.Contains(out, "~/.codex/AGENTS.md (Codex") || len(started) != 1 || started[0].bin != "/bin/codex" {
		t.Errorf("one agent set up, started %v:\n%s", started, out)
	}

	// One agent's CLI: it is named in the question.
	installed["codex"] = false
	if out := setupWith("y\n"); !strings.Contains(out, "Start Claude Code for this now? [Y/n] ") || len(started) != 1 {
		t.Errorf("one agent, started %v:\n%s", started, out)
	}

	// No agent's CLI: the user gets the prompt.
	installed["claude"] = false
	if out := setupWith("\n"); !strings.Contains(out, "start an agent and give it this prompt:\n\nUse the ctxtra skill") || len(started) != 0 {
		t.Errorf("no agent, started %v:\n%s", started, out)
	}

	// A binding with instructions: the config isn't new.
	testenv.Write(t, filepath.Join(h, "notes.md"), "mine\n")
	if code, _, e := run("bind", "notes", "--global", "--file", filepath.Join(h, "notes.md")); code != 0 {
		t.Fatalf("bind: %s", e)
	}
	if out := setupWith("\n"); strings.Contains(out, "instruction files of their own") {
		t.Errorf("asked with instructions bound:\n%s", out)
	}
}

// ctxtra import is the same offer for any config. Without a terminal, or
// with --no-start, it only backs the files up and lists them.
func TestImportCommand(t *testing.T) {
	h := testenv.Home(t)
	isTerminal := interactive
	binary = func() (string, []string, error) { return "/opt/homebrew/bin/ctxtra", nil, nil }
	lookPath = func(name string) (string, error) { return "/bin/" + name, nil }
	var started []string
	startAgent = func(bin string, args []string, dir string) error {
		started = append(started, bin)
		return nil
	}
	t.Cleanup(func() {
		binary, interactive, lookPath, startAgent = setup.Binary, isTerminal, exec.LookPath, runAgent
	})
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
	for _, args := range [][]string{{"import"}, {"import", "--no-start"}} {
		code, out, e := run(args...)
		_, backup, ok := strings.Cut(out, "backed up the files to ~/")
		if code != 0 || !ok || !strings.HasPrefix(out, "instruction files of the agents that are set up:\n  ~/.claude/CLAUDE.md (Claude Code)\n") {
			t.Fatalf("%v: %d %q %q", args, code, out, e)
		}
		if b, err := os.ReadFile(filepath.Join(h, strings.TrimSpace(backup), "claude", ".claude", "CLAUDE.md")); string(b) != "claude rules\n" {
			t.Errorf("%v: backup %q %v", args, b, err)
		}
	}
	if code, _, _ := run("import", "x"); code != 2 {
		t.Errorf("import x: %d", code)
	}
	if len(started) != 0 {
		t.Errorf("started %v without a terminal", started)
	}

	// In a terminal it asks, also with instructions bound; it needs the skill.
	interactive = func(io.Reader) bool { return true }
	importWith := func(answer string) (int, string, string) {
		var o, e bytes.Buffer
		code := Run([]string{"import"}, IO{In: strings.NewReader(answer), Out: &o, Err: &e}, "test")
		return code, o.String(), e.String()
	}
	if code, _, e := importWith("\n"); code != 1 || !strings.Contains(e, "install it first with ctxtra skill install") {
		t.Errorf("without the skill: %d %q", code, e)
	}
	if code, _, e := run("skill", "install"); code != 0 {
		t.Fatalf("skill install: %s", e)
	}
	testenv.Write(t, filepath.Join(h, "notes.md"), "mine\n")
	if code, _, e := run("bind", "notes", "--global", "--file", filepath.Join(h, "notes.md")); code != 0 {
		t.Fatalf("bind: %s", e)
	}
	if code, out, _ := importWith("n\n"); code != 0 || !strings.Contains(out, "Start Claude Code for this now? [Y/n] not started; to do it later: ctxtra import\n") {
		t.Errorf("declined: %d %q", code, out)
	}
	if code, _, _ := importWith("\n"); code != 0 || len(started) != 1 || started[0] != "/bin/claude" {
		t.Errorf("accepted: %d %v", code, started)
	}
	// The prompt names the skill's SKILL.md: import writes the skill again
	// when it is gone and the binding is left.
	if err := os.RemoveAll(filepath.Join(h, ".ctxtra", "skills")); err != nil {
		t.Fatal(err)
	}
	if code, _, e := importWith("\n"); code != 0 {
		t.Errorf("without the skill's files: %d %q", code, e)
	}
	if _, err := os.Stat(filepath.Join(h, ".ctxtra", "skills", "ctxtra", "SKILL.md")); err != nil {
		t.Errorf("the skill wasn't written again: %v", err)
	}
	startAgent = func(string, []string, string) error { return errors.New("exited: exit status 1") }
	if code, _, e := importWith("\n"); code != 1 || !strings.Contains(e, "ctxtra import: Claude Code: exited: exit status 1; you can give the prompt above to an agent") {
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
	code, out, e := run("import", "--no-start")
	_, backup, _ := strings.Cut(out, "backed up the files to ~/")
	if code != 0 || !strings.Contains(out, "  ~/claude/CLAUDE.md (Claude Code)\n") {
		t.Fatalf("import: %d %q %q", code, out, e)
	}
	if b, err := os.ReadFile(filepath.Join(h, strings.TrimSpace(backup), "claude", "claude", "CLAUDE.md")); string(b) != "claude rules\n" {
		t.Errorf("backup: %q %v", b, err)
	}
	env := absEnv([]string{"CLAUDE_CONFIG_DIR=../../claude", "CODEX_HOME=~/codex", "CTXTRA_CONFIG=", "OTHER=x/y"})
	want := []string{"CLAUDE_CONFIG_DIR=" + filepath.Join(h, "claude"), "CODEX_HOME=~/codex", "CTXTRA_CONFIG=", "OTHER=x/y"}
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
