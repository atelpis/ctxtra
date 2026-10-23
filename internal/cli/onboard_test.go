package cli

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/atelpis/ctxtra/internal/state"
	"github.com/atelpis/ctxtra/internal/testenv"
)

func runOnboard(input string, args ...string) (int, string, string) {
	var out, errOut bytes.Buffer
	code := Run(append([]string{"onboard"}, args...), IO{In: strings.NewReader(input), Out: &out, Err: &errOut}, "test")
	return code, out.String(), errOut.String()
}

func TestOnboardImport(t *testing.T) {
	for _, tc := range []struct {
		name, input, started string
	}{
		{"choose codex", "\n\ny\n2\n", "/bin/codex"},
		{"default agent", "\n\n\n\n", "/bin/claude"},
		{"decline import", "y\ny\nn\n", ""},
		{"EOF at import", "y\ny\ny", ""},
		{"EOF at choice", "y\ny\ny\n2", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := testenv.Home(t)
			f := newFakeAgents(t, "claude", "codex")
			file := filepath.Join(h, ".claude", "CLAUDE.md")
			testenv.Write(t, file, "my instructions\n")
			code, out, errOut := runOnboard("y\n"+tc.input, "--agent", "claude", "--agent", "codex")
			if code != 0 {
				t.Fatalf("onboard: %d %s", code, errOut)
			}
			for _, text := range []string{"Your agents can set up ctxtra for you", "Work across multiple projects", "Start an agent to help"} {
				if !strings.Contains(out+errOut, text) {
					t.Errorf("missing %q:\n%s", text, out)
				}
			}
			if strings.Contains(out, "Next:") || skillBound("ctxtra") == nil || skillBound(follow) == nil {
				t.Fatalf("skills or flow:\n%s", out)
			}
			if tc.started == "" {
				if len(f.started) != 0 {
					t.Fatalf("started without consent: %+v", f.started)
				}
				backups, _ := filepath.Glob(filepath.Join(h, ".ctxtra", "backups", "import-*"))
				if len(backups) != 0 {
					t.Fatalf("backed up without consent: %v", backups)
				}
			} else {
				if len(f.started) != 1 || f.started[0].bin != tc.started {
					t.Fatalf("started %+v", f.started)
				}
				backups, _ := filepath.Glob(filepath.Join(h, ".ctxtra", "backups", "import-*", "claude", ".claude", "CLAUDE.md"))
				if len(backups) != 1 {
					t.Fatalf("backups: %v", backups)
				}
				if b, err := os.ReadFile(backups[0]); err != nil || string(b) != "my instructions\n" {
					t.Fatalf("backup: %q %v", b, err)
				}
			}
			if b, err := os.ReadFile(file); err != nil || string(b) != "my instructions\n" {
				t.Fatalf("original changed: %q %v", b, err)
			}
		})
	}
}

func TestOnboardDeclineSkills(t *testing.T) {
	for _, input := range []string{"n\nn\n", "", "y"} {
		t.Run(input, func(t *testing.T) {
			testenv.Home(t)
			f := newFakeAgents(t, "claude")
			if code, _, errOut := runOnboard("y\n"+input, "--agent", "claude"); code != 0 {
				t.Fatalf("onboard: %d %s", code, errOut)
			}
			if skillBound("ctxtra") != nil || skillBound(follow) != nil || len(f.started) != 0 {
				t.Fatal("declined skills installed or agent started")
			}
		})
	}
}

func TestOnboardRerun(t *testing.T) {
	h := testenv.Home(t)
	newFakeAgents(t, "claude")
	if code, _, errOut := runOnboard("\n\n\n", "--agent", "claude"); code != 0 {
		t.Fatal(errOut)
	}
	testenv.Write(t, filepath.Join(h, "notes.md"), "notes")
	if code, _, errOut := run("binding", "add", "notes", "--global", "--instruction", filepath.Join(h, "notes.md")); code != 0 {
		t.Fatal(errOut)
	}
	testenv.Write(t, filepath.Join(h, ".claude", "CLAUDE.md"), "old rules")
	if code, out, errOut := runOnboard("y\n", "--agent", "claude"); code != 0 || strings.Contains(out+errOut, "skill?") {
		t.Fatalf("rerun: %d %s\n%s", code, errOut, out)
	}
}

func TestOnboardNeedsTerminal(t *testing.T) {
	for _, mode := range []string{"no terminal", "no input", "custom config"} {
		t.Run(mode, func(t *testing.T) {
			testenv.Home(t)
			newFakeAgents(t, "claude")
			args := []string{"--agent", "claude"}
			if mode == "no terminal" {
				Interactive = func(IO) bool { return false }
			} else if mode == "no input" {
				args = append(args, "--no-input")
			} else {
				args = append(args, "--config", "other.yaml")
			}
			if code, _, errOut := runOnboard("\n\n\n", args...); code == 0 || errOut == "" {
				t.Fatalf("expected rejection: %d %s", code, errOut)
			}
			if _, err := os.Stat(state.SetupsPath()); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("setup changed: %v", err)
			}
			if skillBound("ctxtra") != nil {
				t.Fatal("skill installed")
			}
		})
	}
}

func TestOnboardImportAvailability(t *testing.T) {
	for _, tc := range []struct {
		name, input string
		installed   []string
		started     int
	}{
		{"single agent", "\n\n\n", []string{"claude"}, 1},
		{"no launcher", "\n\n", nil, 0},
		{"declined base skill", "n\ny\n", []string{"claude"}, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := testenv.Home(t)
			f := newFakeAgents(t, tc.installed...)
			testenv.Write(t, filepath.Join(h, ".claude", "CLAUDE.md"), "rules")
			code, out, errOut := runOnboard("y\n"+tc.input, "--agent", "claude")
			if code != 0 || len(f.started) != tc.started || strings.Contains(out+errOut, "Which agent?") {
				t.Fatalf("onboard: %d %s, started %+v\n%s", code, errOut, f.started, out)
			}
			if tc.started == 0 && !strings.Contains(out, "run ctxtra import") {
				t.Fatalf("no import instructions:\n%s", out)
			}
		})
	}
}

func TestOnboardFailure(t *testing.T) {
	for _, mode := range []string{"setup", "skill", "choice", "start"} {
		t.Run(mode, func(t *testing.T) {
			h := testenv.Home(t)
			f := newFakeAgents(t, "claude", "codex")
			testenv.Write(t, filepath.Join(h, ".claude", "CLAUDE.md"), "rules")
			args := []string{"--agent", "claude", "--agent", "codex"}
			input := "\n\ny\n1\n"
			switch mode {
			case "setup":
				args = []string{"--agent", "nonexistent"}
			case "skill":
				testenv.Write(t, filepath.Join(h, "notes.md"), "notes")
				testenv.Config(t, h, "schema: ctxtra/v1\nbindings:\n  - name: ctxtra\n    match: {global: true}\n    instructions: [~/notes.md]\n")
			case "choice":
				input = "\n\ny\n99\n"
			case "start":
				startAgent = func(string, []string, string) error { return errors.New("cannot start agent") }
			}
			code, _, errOut := runOnboard("y\n"+input, args...)
			if code == 0 || errOut == "" || len(f.started) != 0 {
				t.Fatalf("onboard: %d %s, started %+v", code, errOut, f.started)
			}
			if mode == "setup" && skillBound("ctxtra") != nil {
				t.Fatal("installed skill after failed setup")
			}
			if mode == "choice" {
				backups, _ := filepath.Glob(filepath.Join(h, ".ctxtra", "backups", "import-*"))
				if len(backups) != 0 {
					t.Fatalf("backup before valid choice: %v", backups)
				}
			}
		})
	}
}

func TestOnboardDeclineHooks(t *testing.T) {
	for _, input := range []string{"n\ny\ny\n", "", "y"} {
		t.Run(input, func(t *testing.T) {
			h := testenv.Home(t)
			f := newFakeAgents(t, "claude")
			config := filepath.Join(h, ".claude", "settings.json")
			testenv.Write(t, config, "{}\n")
			code, out, errOut := runOnboard(input, "--agent", "claude")
			if code != 0 || !strings.Contains(out, "~/.claude/settings.json") || !strings.Contains(errOut, "Install hooks and continue? [Y/n]") || !strings.Contains(out, "Onboarding stopped") {
				t.Fatalf("decline: %d %s\n%s", code, errOut, out)
			}
			if b, err := os.ReadFile(config); err != nil || string(b) != "{}\n" {
				t.Fatalf("config changed: %q %v", b, err)
			}
			if _, err := os.Stat(filepath.Join(h, ".ctxtra")); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("onboarding wrote state: %v", err)
			}
			if skillBound("ctxtra") != nil || skillBound(follow) != nil || len(f.started) != 0 || strings.Contains(out, "Your agents can set up") {
				t.Fatal("continued after declining hooks")
			}
		})
	}
}

func TestOnboardStepSeparators(t *testing.T) {
	testenv.Home(t)
	newFakeAgents(t, "claude")
	code, out, errOut := runOnboard("y\nn\nn\n", "--agent", "claude")
	if code != 0 {
		t.Fatal(errOut)
	}
	steps := strings.Split(out, "\n------------------------------------------------------------\n\n")
	if len(steps) != 4 || !strings.Contains(steps[0], "ctxtra is set up") || !strings.HasPrefix(steps[1], "Your agents can set up") || !strings.HasPrefix(steps[2], "Work across multiple projects") || !strings.HasPrefix(steps[3], "Your agents have no existing") {
		t.Fatalf("step boundaries:\n%s", out)
	}
}
