package cli

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/atelpis/ctxtra/internal/setup"
	"github.com/atelpis/ctxtra/internal/testenv"
)

func run(args ...string) (code int, out, errOut string) {
	var o, e bytes.Buffer
	code = Run(args, IO{In: strings.NewReader(""), Out: &o, Err: &e}, "test")
	return code, o.String(), e.String()
}

func TestCommands(t *testing.T) {
	h := testenv.Home(t)
	r := testenv.Repo(t, filepath.Join(h, "svc"), "git@github.com:acme/svc.git")
	testenv.Write(t, filepath.Join(h, "a.md"), "A")
	testenv.Write(t, filepath.Join(h, ".config", "ctxtra", "b.yaml"), `schema: ctxtra/v1
bindings:
  - name: svc
    match: {repos: [{remote: github.com/acme/svc}]}
    instructions: [~/a.md, ~/gone.md]
`)
	testenv.Config(t, h, "schema: ctxtra/v1\ninclude: [b.yaml]\n")

	if code, out, errOut := run("files", r); code != 1 || out != filepath.Join(h, "a.md")+"\n" || !strings.Contains(errOut, "missing: "+filepath.Join(h, "gone.md")) {
		t.Errorf("files: %d %q %q", code, out, errOut)
	}
	if code, out, _ := run("show", r); code != 0 || !strings.Contains(out, "Contents of "+filepath.Join(h, "a.md")) {
		t.Errorf("show: %d %q", code, out)
	}
	if _, out, _ := run("explain", h); !strings.Contains(out, "- svc (~/.config/ctxtra/b.yaml): repos: not in a git repository") {
		t.Errorf("explain outside git:\n%s", out)
	}
	if _, out, _ := run("explain", r); !strings.Contains(out, "+ svc (~/.config/ctxtra/b.yaml): repos remote github.com/acme/svc") ||
		!strings.Contains(out, "MISSING  "+filepath.Join(h, "gone.md")) {
		t.Errorf("explain:\n%s", out)
	}
	if code, out, _ := run("doctor", r); code != 1 || !strings.Contains(out, "claude: not set up") || !strings.Contains(out, "MISSING") {
		t.Errorf("doctor: %d\n%s", code, out)
	}
	if code, out, _ := run("hook", "claude", "SessionStart", "--slot", "0"); code != 0 || out != "{}\n" {
		t.Errorf("hook without input: %d %q", code, out)
	}
	for _, args := range [][]string{{"nope"}, {"files", "a", "b"}, {"setup", "--agent"}, {"setup", "extra"}, {"install"}} {
		if code, _, _ := run(args...); code != 2 {
			t.Errorf("%v: exit %d, want 2", args, code)
		}
	}
	binary = func() (string, []string, error) { return "/opt/homebrew/bin/ctxtra", nil, nil }
	t.Cleanup(func() { binary = setup.Binary })
	for _, args := range [][]string{{"setup", "-h"}, {"files", "--help"}, {"help"}} {
		if code, _, _ := run(args...); code != 0 {
			t.Errorf("%v: exit %d, want 0", args, code)
		}
	}
	if code, out, _ := run("setup", "--agent", "claude", "--dry-run"); code != 0 || !strings.Contains(out, "Setup would change these files:\n  Claude Code   ~/.claude/settings.json\n") {
		t.Errorf("setup dry run: %d %q", code, out)
	}
}

// explain warns about each agent whose sessions wouldn't get every file,
// by that agent's limit: Codex and Antigravity count bytes, Copilot
// estimated tokens, the others characters.
func TestSizeWarnings(t *testing.T) {
	h := testenv.Home(t)
	testenv.Write(t, filepath.Join(h, ".config", "ctxtra", "b.yaml"),
		"schema: ctxtra/v1\nbindings:\n  - {name: me, match: {global: true}, instructions: [~/a.md, ~/b.md]}\n")
	testenv.Config(t, h, "schema: ctxtra/v1\ninclude: [b.yaml]\n")
	testenv.Write(t, filepath.Join(h, "a.md"), "small")
	for _, tc := range []struct {
		content string
		warn    []string
	}{
		{"short", nil},
		{strings.Repeat("a\n", 75_000), []string{"copilot"}}, // 150,000 bytes
		{strings.Repeat("a\n", 200_000), []string{"claude", "muse", "copilot"}},
		{strings.Repeat("界\n", 300_000), []string{"claude", "muse", "codex", "antigravity", "copilot"}}, // 1,200,000 bytes
	} {
		testenv.Write(t, filepath.Join(h, "b.md"), tc.content)
		_, out, _ := run("explain", h)
		for _, a := range []string{"claude", "muse", "codex", "antigravity", "copilot", "pi", "hermes"} {
			warned := strings.Contains(out, ", "+a+" sessions don't get b.md\n")
			if warned != slices.Contains(tc.warn, a) {
				t.Errorf("%d bytes: %s warned %v:\n%s", len(tc.content), a, warned, out)
			}
		}
		if slices.Contains(tc.warn, "claude") && !strings.Contains(out, "WARNING: over Claude Code's limit of 360,000 characters, claude sessions don't get b.md\n") {
			t.Errorf("warning:\n%s", out)
		}
	}
}

// In a terminal, setup offers each skill that isn't installed, with a
// question of its own; the answer decides.
func TestSetupOffersSkill(t *testing.T) {
	h := testenv.Home(t)
	isTerminal := interactive
	binary = func() (string, []string, error) { return "/opt/homebrew/bin/ctxtra", nil, nil }
	interactive = func(io.Reader) bool { return true }
	t.Cleanup(func() { binary, interactive = setup.Binary, isTerminal })
	setupWith := func(answer string) string {
		var o, e bytes.Buffer
		if code := Run([]string{"setup", "--agent", "claude"}, IO{In: strings.NewReader(answer), Out: &o, Err: &e}, "test"); code != 0 {
			t.Fatalf("setup: %d %s", code, e.String())
		}
		return o.String()
	}
	const base, follow = "Your agents can set up ctxtra for you\n", "Work across multiple projects in one session?\n"
	for _, answer := range []string{"n\nn\n", "no\nx\n", "n\n", ""} {
		out := setupWith(answer)
		if !strings.Contains(out, base) || !strings.Contains(out, follow) || strings.Count(out, " skill? [Y/n] Skipped. To install it later: ctxtra skill install") != 2 {
			t.Errorf("declined with %q:\n%s", answer, out)
		}
	}
	if _, err := os.Stat(filepath.Join(h, ".ctxtra", "skills")); err == nil {
		t.Error("declined, but a skill was written")
	}
	want := `ctxtra is already set up for Claude Code.

Your agents can set up ctxtra for you
ctxtra comes with a skill that makes getting started easy. Your agents can create your bindings, change your config, and bring the instructions you already have into ctxtra.

Install the ctxtra skill? [Y/n] 
Installed to ~/.ctxtra/skills/ctxtra
Created ~/.config/ctxtra/config.yaml
The skill is ready for new agent sessions in any folder. Ask your agent to help you get started with ctxtra.

Work across multiple projects in one session?
The ctxtra-follow skill helps your agent pick up the instructions and skills you've configured for each project as your work moves between them.

Install the ctxtra-follow skill? [Y/n] Skipped. To install it later: ctxtra skill install ctxtra-follow
`
	if out := setupWith("\nn\n"); out != want {
		t.Errorf("accepted the first with Enter, declined the second:\n%s\nwant:\n%s", out, want)
	}
	want = `ctxtra is already set up for Claude Code.

Work across multiple projects in one session?
The ctxtra-follow skill helps your agent pick up the instructions and skills you've configured for each project as your work moves between them.

Install the ctxtra-follow skill? [Y/n] 
Installed to ~/.ctxtra/skills/ctxtra-follow
Added to ~/.config/ctxtra/config.yaml
The skill is ready for new agent sessions in any folder.
`
	if out := setupWith("\n"); out != want {
		t.Errorf("accepted the second with Enter:\n%s\nwant:\n%s", out, want)
	}
	if out := setupWith(""); strings.Contains(out, "skill? [Y/n]") {
		t.Errorf("installed, but asked again:\n%s", out)
	}
}

// The hook reads --slot and --of wherever they are and ignores other
// arguments; options it can't read add nothing, and are logged.
func TestHookArgs(t *testing.T) {
	for _, c := range []struct {
		args        string
		slot, slots int
		err         string
	}{
		{"", -1, 0, ""},
		{"--slot 3", 3, 0, ""},
		{"--slot 3 --of 40 --future x", 3, 40, ""},
		{"--future --slot 3", 3, 0, ""},
		{"--of 40 --part-size 12000 --slot 39", 39, 40, ""},
		{"--slot x", 0, 0, `--slot "x" is not a non-negative integer`},
		{"--slot -1", 0, 0, `--slot "-1" is not a non-negative integer`},
		{"--slot +1", 0, 0, `--slot "+1" is not a non-negative integer`},
		{"--slot 1 --slot 2", 0, 0, "--slot given twice"},
		{"--slot 1 --of 4 --of 4", 0, 0, "--of given twice"},
		{"--slot", 0, 0, "--slot without a value"},
		{"--of 40", 0, 0, "--of without --slot"},
		{"--slot 0 --of 0", 0, 0, "--of 0"},
		{"--slot 40 --of 40", 0, 0, "--slot 40 not below --of 40"},
	} {
		got := hookArgs(strings.Fields(c.args))
		switch {
		case c.err != "":
			if got.Err == nil || got.Err.Error() != c.err {
				t.Errorf("%q: error %v, want %q", c.args, got.Err, c.err)
			}
		case got.Err != nil || got.Slot != c.slot || got.Slots != c.slots:
			t.Errorf("%q: %+v, want slot %d of %d", c.args, got, c.slot, c.slots)
		}
	}
	h := testenv.Home(t)
	for _, args := range [][]string{{"--slot", "x"}, {"--slot", "1", "--slot", "2"}} {
		code, out, _ := run(append([]string{"hook", "claude", "SessionStart"}, args...)...)
		if code != 0 || out != "{}\n" {
			t.Errorf("%v: %d %q", args, code, out)
		}
	}
	if b, _ := os.ReadFile(filepath.Join(h, ".ctxtra", "log.jsonl")); strings.Count(string(b), "bad hook arguments") != 2 {
		t.Errorf("log:\n%s", b)
	}
}
