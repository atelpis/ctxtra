package e2e

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/atelpis/ctxtra/e2e/driver"
	"github.com/atelpis/ctxtra/e2e/mock"
)

// Codex's interactive TUI, driven through tmux: the user trusts ctxtra's
// hooks once with "Trust all and continue" in the hook review, the session
// then gets every line of the bound files, and running setup again changes
// nothing that Codex would ask to trust again.
func TestCodexTrustTUI(t *testing.T) {
	forEachAgent(t, nil, func(t *testing.T, a Agent) {
		if a.Name() != "codex" {
			t.Skip("Codex only")
		}
		tmux := tmuxBin(t)
		s := newSession(t, a)
		repo := s.Repo("dev/svc", "", nil)
		files := capacityFiles(s)
		s.Setup()
		checkCodexLimits(t, s.Path(".codex/hooks.json"))

		m := mock.New(t)
		run := codexTUIScript(t, s, a, repo, m.URL())
		tui := startTUI(t, tmux, run)
		if !tui.untilReady(t) {
			t.Fatal("Codex didn't ask to review the hooks")
		}
		tui.prompt(t, m, 0)
		complete(t, "main[first]", firstMain(t, m, 0), files...)
		tui.stop()

		s.Setup()
		seen := len(m.Requests())
		tui = startTUI(t, tmux, run)
		if tui.untilReady(t) {
			t.Error("Codex asked to review the hooks again after setup ran again")
		}
		tui.prompt(t, m, seen)
		complete(t, "main[second session]", firstMain(t, m, seen), files...)
		tui.stop()
	})
}

// An upgrade replaces the binary at the path that the hooks run, as go
// install does: Codex still trusts ctxtra's hooks, so it doesn't ask to
// review them again, and the session gets every line of the bound files.
func TestCodexUpgradeKeepsTrust(t *testing.T) {
	forEachAgent(t, nil, func(t *testing.T, a Agent) {
		if a.Name() != "codex" {
			t.Skip("Codex only")
		}
		tmux := tmuxBin(t)
		s := newSession(t, a)
		repo := s.Repo("dev/svc", "", nil)
		files := capacityFiles(s)
		bin := s.Path("bin/ctxtra")
		b, err := os.ReadFile(ctxtraBin)
		if err != nil {
			t.Fatal(err)
		}
		s.Write("bin/ctxtra", string(b))
		if err := os.Chmod(bin, 0o755); err != nil {
			t.Fatal(err)
		}
		ctxtra := func(args ...string) string {
			t.Helper()
			cmd := exec.Command(bin, args...)
			cmd.Dir, cmd.Env = s.Home, s.Env()
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("ctxtra %s: %v\n%s", strings.Join(args, " "), err, out)
			}
			return string(out)
		}
		ctxtra("setup", "--agent", "codex")
		if hooks, _ := os.ReadFile(s.Path(".codex/hooks.json")); !strings.Contains(string(hooks), `\"$HOME/bin/ctxtra\"`) {
			t.Fatalf("the hooks don't run $HOME/bin/ctxtra:\n%s", hooks)
		}

		m := mock.New(t)
		run := codexTUIScript(t, s, a, repo, m.URL())
		tui := startTUI(t, tmux, run)
		if !tui.untilReady(t) {
			t.Fatal("Codex didn't ask to review the hooks")
		}
		tui.prompt(t, m, 0)
		complete(t, "main[first]", firstMain(t, m, 0), files...)
		tui.stop()

		before := ctxtra("version")
		newer := s.Path("tmp/ctxtra-newer")
		build := exec.Command("go", "build", "-ldflags", "-X main.version=e2e-newer", "-o", newer, "./cmd/ctxtra")
		build.Dir = ".."
		if out, err := build.CombinedOutput(); err != nil {
			t.Fatalf("building ctxtra: %v\n%s", err, out)
		}
		if err := os.Rename(newer, bin); err != nil {
			t.Fatal(err)
		}
		if after := strings.TrimSpace(ctxtra("version")); after != "e2e-newer" || after == strings.TrimSpace(before) {
			t.Fatalf("version %q after the upgrade, %q before", after, before)
		}
		seen := len(m.Requests())
		tui = startTUI(t, tmux, run)
		if tui.untilReady(t) {
			t.Error("Codex asked to review the hooks again after the upgrade")
		}
		tui.prompt(t, m, seen)
		complete(t, "main[after the upgrade]", firstMain(t, m, seen), files...)
		tui.stop()
	})
}

// checkCodexLimits checks that ctxtra's hooks set a finite
// additionalContextLimit, in tokens, that covers the capacity at about 4
// characters per token.
func checkCodexLimits(t *testing.T, path string) {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var cfg struct {
		Hooks map[string][]struct {
			Hooks []map[string]any `json:"hooks"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal(b, &cfg); err != nil {
		t.Fatal(err)
	}
	for _, ev := range []string{"SessionStart", "SubagentStart"} {
		n := 0
		for _, g := range cfg.Hooks[ev] {
			for _, h := range g.Hooks {
				if cmd, _ := h["command"].(string); !strings.Contains(cmd, "ctxtra") {
					continue
				}
				n++
				limit, ok := h["additionalContextLimit"].(float64)
				if !ok || limit <= 0 || limit*4 < float64(capacity("codex")) {
					t.Errorf("%s: additionalContextLimit %v, want a finite limit of at least %d tokens", ev, h["additionalContextLimit"], capacity("codex")/4)
				}
			}
		}
		if n != 1 {
			t.Errorf("%s: %d ctxtra hooks, want 1", ev, n)
		}
	}
}

// codexTUIScript writes a script that starts interactive Codex in repo with
// the fixture's environment and the mock as its model, without bypassing
// hook trust.
func codexTUIScript(t *testing.T, s *Session, a Agent, repo, mockURL string) string {
	t.Helper()
	cmd, err := a.Command(a.Bin, s.Home, repo, mockURL, mock.Prompt, driver.Options{Untrusted: true}, s.Env())
	if err != nil {
		t.Fatal(err)
	}
	var args []string
	for _, arg := range cmd.Args[:len(cmd.Args)-1] { // without the prompt
		switch arg {
		case "exec", "--skip-git-repo-check", "--ephemeral", "--json":
			continue
		}
		args = append(args, arg)
	}
	args = append(args, "-c", "check_for_update_on_startup=false")
	q := func(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }
	var b strings.Builder
	b.WriteString("#!/bin/sh\ncd " + q(repo) + " || exit 1\nexec env -i")
	for _, e := range cmd.Env {
		if strings.HasPrefix(e, "TERM=") {
			e = "TERM=xterm-256color"
		}
		b.WriteString(" " + q(e))
	}
	for _, arg := range args {
		b.WriteString(" " + q(arg))
	}
	b.WriteString("\n")
	path := s.Write("codex-tui.sh", b.String())
	if err := os.Chmod(path, 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func tmuxBin(t *testing.T) string {
	bin, err := exec.LookPath("tmux")
	if err != nil {
		if os.Getenv("CTXTRA_E2E_REQUIRE") != "" {
			t.Fatal("tmux not found")
		}
		t.Skip("tmux not found")
	}
	return bin
}

// tui is an agent's TUI in a detached tmux session.
type tui struct {
	tmux, name string
}

var tuiSessions atomic.Int64 // subtests run in parallel

func startTUI(t *testing.T, tmux, script string) *tui {
	t.Helper()
	x := &tui{tmux: tmux, name: fmt.Sprintf("ctxtra-e2e-%d-%d", os.Getpid(), tuiSessions.Add(1))}
	if out, err := exec.Command(tmux, "new-session", "-d", "-s", x.name, "-x", "200", "-y", "50", script).CombinedOutput(); err != nil {
		t.Fatalf("tmux: %v\n%s", err, out)
	}
	t.Cleanup(x.stop)
	return x
}

// stop ends the session, and waits until the processes in it are gone.
// tmux starts the pane in a session of its own: its PID is the group's.
func (x *tui) stop() {
	out, _ := exec.Command(x.tmux, "list-panes", "-t", x.name, "-F", "#{pane_pid}").Output()
	exec.Command(x.tmux, "kill-session", "-t", x.name).Run()
	if pid, err := strconv.Atoi(strings.TrimSpace(string(out))); err == nil {
		endGroup(pid)
	}
}

func (x *tui) screen() string {
	out, _ := exec.Command(x.tmux, "capture-pane", "-p", "-t", x.name).Output()
	return string(out)
}

func (x *tui) keys(keys ...string) {
	exec.Command(x.tmux, append([]string{"send-keys", "-t", x.name}, keys...)...).Run()
}

// untilReady answers Codex's startup questions until the prompt is ready:
// it trusts the folder and trusts all hooks. It reports whether the hook
// review came up.
func (x *tui) untilReady(t *testing.T) (reviewed bool) {
	t.Helper()
	// Codex draws its prompt first and the questions over it a moment
	// later: the prompt is ready once no question came up for a while.
	var idle time.Time
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		sc := x.screen()
		switch {
		case strings.Contains(sc, "Trust this folder?"):
			idle = time.Time{}
			x.keys("Enter") // 1. Trust and continue
		case strings.Contains(sc, "Hooks need review"):
			if !strings.Contains(sc, "Trust all and continue") {
				t.Fatalf("hook review without \"Trust all and continue\":\n%s", sc)
			}
			idle = time.Time{}
			reviewed = true
			x.keys("Down", "Enter") // 2. Trust all and continue
		case strings.Contains(sc, "Ask Codex to do anything"):
			if idle.IsZero() {
				idle = time.Now()
			} else if time.Since(idle) > 3*time.Second {
				return reviewed
			}
		}
		time.Sleep(300 * time.Millisecond)
	}
	t.Fatalf("Codex's prompt didn't come up:\n%s", x.screen())
	return false
}

// prompt submits the mock's task and waits for the main model request
// after the first seen requests.
func (x *tui) prompt(t *testing.T, m *mock.Server, seen int) {
	t.Helper()
	x.keys("-l", mock.Prompt)
	for try := 0; try < 3; try++ {
		x.keys("Enter")
		for end := time.Now().Add(10 * time.Second); time.Now().Before(end); time.Sleep(200 * time.Millisecond) {
			for _, q := range m.Requests()[seen:] {
				if q.Role == mock.Main {
					return
				}
			}
		}
	}
	t.Fatalf("no model request after the prompt:\n%s", x.screen())
}

func firstMain(t *testing.T, m *mock.Server, seen int) string {
	t.Helper()
	for _, q := range m.Requests()[seen:] {
		if q.Role == mock.Main {
			return q.Text
		}
	}
	t.Fatal("no main request")
	return ""
}
