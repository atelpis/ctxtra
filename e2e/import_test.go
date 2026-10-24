package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/atelpis/ctxtra/e2e/driver"
	"github.com/atelpis/ctxtra/e2e/mock"
)

// importFile is, per agent, a user-level instruction file that ctxtra
// import moves. Hermes has none: its run moves Claude Code's.
var importFile = map[string]string{
	"claude": ".claude/CLAUDE.md", "codex": ".codex/AGENTS.md", "pi": ".pi/agent/AGENTS.md",
	"opencode": ".config/opencode/AGENTS.md", "copilot": ".copilot/copilot-instructions.md",
	"antigravity": ".gemini/GEMINI.md", "muse": ".config/muse/AGENTS.md", "dsh": ".dsh/AGENTS.md",
	"hermes": ".claude/CLAUDE.md",
}

// ctxtra import --start in a terminal starts the agent's interactive app
// with the import prompt (DSH has none: import prints the prompt, which the
// test gives to its headless app). The agent's first request has the prompt, with the agent's
// own instruction file and its backup, and the ctxtra skill. The mock
// checks what ctxtra hands over, not what a real model makes of it.
func TestImportTUI(t *testing.T) {
	forEachAgent(t, nil, func(t *testing.T, a Agent) {
		if a.Name() == "claude" {
			t.Skip("Disabled until Claude's interactive startup handling is fixed")
		}
		tmux := tmuxBin(t)
		s := newSession(t, a)
		file := importFile[a.Name()]
		s.Marked(file, "own")
		owner := a.Name()
		agents := []string{a.Name()}
		if a.Name() == "hermes" {
			owner = "claude"
			agents = append(agents, owner)
		}
		// Set up once without a terminal first, so that the agent's config
		// sees what setup wrote (Hermes enables the plugin).
		for _, name := range agents {
			s.Ctxtra("setup", "--agent", name)
		}
		s.Ctxtra("skill", "install", "ctxtra")
		m := mock.New(t)
		ui, isTUI := a.Driver.(driver.TUI)
		tui := startTUI(t, tmux, importScript(t, s, a, m.URL()))
		t.Cleanup(func() {
			if t.Failed() {
				t.Logf("screen:\n%s", tui.screen())
			}
		})

		var text string
		if isTUI {
			deadline := time.Now().Add(120 * time.Second)
			for !hasMain(m) {
				if time.Now().After(deadline) {
					t.Fatalf("%s sent no request with the prompt", a.Name())
				}
				if keys := ui.Answer(tui.screen()); keys != nil {
					tui.keys(keys...)
				}
				time.Sleep(500 * time.Millisecond)
			}
			text = firstMain(t, m, 0)
		} else {
			// No app to start: import prints the prompt for the user.
			tui.until(t, "ctxtra backed them up to")
			text = s.Run(t.TempDir(), driver.Options{Prompt: printedPrompt(t, tui)}).First(mock.Main).text
		}
		for _, want := range []string{
			`Use the ctxtra skill (~/.ctxtra/skills/ctxtra/SKILL.md), task "Import existing instructions".`,
			"- ~/" + file + " (",
			"ctxtra backed them up to ~/.ctxtra/backups/import-",
			"Set up and change ctxtra", // the skill's description, from the hook
		} {
			if !strings.Contains(text, want) {
				t.Errorf("the first request lacks %q:\n%s", want, clip(text, 4000))
			}
		}
		backups, _ := filepath.Glob(s.Path(".ctxtra/backups/import-*/" + owner + "/" + file))
		if len(backups) != 1 {
			t.Fatalf("backups %v", backups)
		}
		if b, err := os.ReadFile(backups[0]); err != nil || string(b) != "CTXMARK-own\n" {
			t.Errorf("backup %q %v", b, err)
		}
		tui.stop()
	})
}

// importScript writes a script that runs ctxtra import in the fixture's
// environment: with --start for an agent with an interactive app, which a
// shim in PATH runs against the mock; import starts it with the prompt only.
func importScript(t *testing.T, s *Session, a Agent, mockURL string) string {
	t.Helper()
	env := s.Env()
	q := func(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }
	if ui, ok := a.Driver.(driver.TUI); ok {
		app, err := ui.Interactive(a.Bin, s.Home, mockURL, env)
		if err != nil {
			t.Fatal(err)
		}
		var shim strings.Builder
		shim.WriteString("#!/bin/sh\nexec " + q(a.Bin))
		for _, arg := range app.Pre {
			shim.WriteString(" " + q(arg))
		}
		shim.WriteString(` "$@"`)
		for _, arg := range app.Post {
			shim.WriteString(" " + q(arg))
		}
		shim.WriteString("\n")
		executable(t, s.Write("shim/"+a.Executable(), shim.String()))
		env = app.Env
	}
	var b strings.Builder
	// The session stays after import, which can end with the prompt to read.
	b.WriteString("#!/bin/sh\ncd " + q(s.Home) + " || exit 1\nenv -i")
	for _, e := range env {
		switch {
		case strings.HasPrefix(e, "TERM="):
			e = "TERM=xterm-256color"
		case strings.HasPrefix(e, "PATH="):
			e = "PATH=" + s.Path("shim") + string(os.PathListSeparator) + filepath.Dir(ctxtraBin) +
				string(os.PathListSeparator) + strings.TrimPrefix(e, "PATH=")
		}
		b.WriteString(" " + q(e))
	}
	b.WriteString(" " + q(ctxtraBin) + " import")
	if _, ok := a.Driver.(driver.TUI); ok {
		b.WriteString(" --start " + a.Name())
	}
	b.WriteString("\nsleep 3600\n")
	path := s.Write("setup-tui.sh", b.String())
	executable(t, path)
	return path
}

// printedPrompt is the prompt that import printed, from its first line to
// the line that names the backup, with wrapped lines joined.
func printedPrompt(t *testing.T, x *tui) string {
	t.Helper()
	out, _ := exec.Command(x.tmux, "capture-pane", "-p", "-J", "-t", x.name).Output()
	sc := string(out)
	i := strings.Index(sc, "Use the ctxtra skill")
	j := strings.Index(sc, "ctxtra backed them up to")
	if i < 0 || j < i {
		t.Fatalf("no prompt on the screen:\n%s", sc)
	}
	end := strings.IndexByte(sc[j:], '\n')
	if end < 0 {
		end = len(sc) - j
	}
	return strings.TrimSpace(sc[i : j+end])
}

func executable(t *testing.T, path string) {
	t.Helper()
	if err := os.Chmod(path, 0o755); err != nil {
		t.Fatal(err)
	}
}

func hasMain(m *mock.Server) bool {
	for _, q := range m.Requests() {
		if q.Role == mock.Main {
			return true
		}
	}
	return false
}

// until waits for text on the screen.
func (x *tui) until(t *testing.T, text string) {
	t.Helper()
	for end := time.Now().Add(30 * time.Second); time.Now().Before(end); time.Sleep(200 * time.Millisecond) {
		if strings.Contains(x.screen(), text) {
			return
		}
	}
	t.Fatalf("%q didn't come up:\n%s", text, x.screen())
}
