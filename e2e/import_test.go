package e2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/atelpis/ctxtra/e2e/driver"
	"github.com/atelpis/ctxtra/e2e/mock"
)

// ctxtra setup in a terminal: Enter installs the skill and accepts the
// import, and setup starts Codex with the import prompt. Codex's first
// request has the prompt, with Codex's own instruction file and its backup,
// and the ctxtra skill. The mock checks what ctxtra hands over, not what a
// real model makes of it.
func TestImportTUI(t *testing.T) {
	forEachAgent(t, nil, func(t *testing.T, a Agent) {
		if a.Name() != "codex" {
			t.Skip("Codex only")
		}
		tmux := tmuxBin(t)
		s := newSession(t, a)
		s.Marked(".codex/AGENTS.md", "codex-own")
		m := mock.New(t)
		tui := startTUI(t, tmux, setupTUIScript(t, s, a, m.URL()))

		tui.until(t, "Install it now? [Y/n]")
		tui.keys("Enter")
		tui.until(t, "Start Codex for this now? [Y/n]")
		tui.keys("Enter")
		// Codex asks to trust the folder and the hooks, then sends the prompt.
		deadline := time.Now().Add(90 * time.Second)
		for !hasMain(m) {
			if time.Now().After(deadline) {
				t.Fatalf("Codex sent no request with the prompt:\n%s", tui.screen())
			}
			switch sc := tui.screen(); {
			case strings.Contains(sc, "Trust this folder?"):
				tui.keys("Enter")
			case strings.Contains(sc, "Hooks need review"):
				tui.keys("Down", "Enter")
			}
			time.Sleep(500 * time.Millisecond)
		}
		text := firstMain(t, m, 0)
		for _, want := range []string{
			`Use the ctxtra skill (~/.ctxtra/skills/ctxtra/SKILL.md), task "Import existing instructions".`,
			"- ~/.codex/AGENTS.md (Codex)",
			"ctxtra backed them up to ~/.ctxtra/backups/import-",
			"Set up and change ctxtra", // the skill's description, from the hook
		} {
			if !strings.Contains(text, want) {
				t.Errorf("the first request lacks %q:\n%s", want, clip(text, 4000))
			}
		}
		backups, _ := filepath.Glob(s.Path(".ctxtra/backups/import-*/codex/.codex/AGENTS.md"))
		if len(backups) != 1 {
			t.Fatalf("backups %v", backups)
		}
		if b, err := os.ReadFile(backups[0]); err != nil || string(b) != "CTXMARK-codex-own\n" {
			t.Errorf("backup %q %v", b, err)
		}
		tui.stop()
	})
}

// setupTUIScript writes a script that runs ctxtra setup for Codex in the
// fixture's environment, with a codex in PATH that runs the real one
// against the mock: setup starts the agent with the prompt only.
func setupTUIScript(t *testing.T, s *Session, a Agent, mockURL string) string {
	t.Helper()
	cmd, err := a.Command(a.Bin, s.Home, s.Home, mockURL, mock.Prompt, driver.Options{Untrusted: true}, s.Env())
	if err != nil {
		t.Fatal(err)
	}
	q := func(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }
	var shim strings.Builder
	shim.WriteString("#!/bin/sh\nexec " + q(a.Bin))
	for _, arg := range cmd.Args[1 : len(cmd.Args)-1] { // without the binary and the prompt
		switch arg {
		case "exec", "--skip-git-repo-check", "--ephemeral", "--json":
			continue
		}
		shim.WriteString(" " + q(arg))
	}
	shim.WriteString(" -c check_for_update_on_startup=false \"$@\"\n")
	executable(t, s.Write("shim/codex", shim.String()))

	var b strings.Builder
	b.WriteString("#!/bin/sh\ncd " + q(s.Home) + " || exit 1\nexec env -i")
	for _, e := range cmd.Env {
		switch {
		case strings.HasPrefix(e, "TERM="):
			e = "TERM=xterm-256color"
		case strings.HasPrefix(e, "PATH="):
			e = "PATH=" + s.Path("shim") + string(os.PathListSeparator) + filepath.Dir(ctxtraBin) +
				string(os.PathListSeparator) + strings.TrimPrefix(e, "PATH=")
		}
		b.WriteString(" " + q(e))
	}
	b.WriteString(" " + q(ctxtraBin) + " setup --agent codex\n")
	path := s.Write("setup-tui.sh", b.String())
	executable(t, path)
	return path
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
