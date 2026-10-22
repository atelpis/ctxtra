package cli

import (
	"bufio"
	"bytes"
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

	if code, out, errOut := run("context", "files", r); code != 1 || out != filepath.Join(h, "a.md")+"\n" || !strings.Contains(errOut, "missing: "+filepath.Join(h, "gone.md")) {
		t.Errorf("files: %d %q %q", code, out, errOut)
	}
	if code, out, _ := run("context", "show", r); code != 1 || !strings.Contains(out, "Contents of "+filepath.Join(h, "a.md")) {
		t.Errorf("show: %d %q", code, out)
	}
	if code, out, _ := run("context", "explain", h); code != 0 || !strings.Contains(out, "- svc (~/.config/ctxtra/b.yaml): repos: not in a git repository") {
		t.Errorf("explain outside git: %d\n%s", code, out)
	}
	if code, out, _ := run("context", "explain", r); code != 1 || !strings.Contains(out, "+ svc (~/.config/ctxtra/b.yaml): repos remote github.com/acme/svc") ||
		!strings.Contains(out, "MISSING  "+filepath.Join(h, "gone.md")) {
		t.Errorf("explain: %d\n%s", code, out)
	}
	if code, out, _ := run("doctor", r); code != 1 || !strings.Contains(out, "claude: not set up") || !strings.Contains(out, "MISSING") {
		t.Errorf("doctor: %d\n%s", code, out)
	}
	if code, out, _ := run("hook", "claude", "SessionStart", "--slot", "0"); code != 0 || out != "{}\n" {
		t.Errorf("hook without input: %d %q", code, out)
	}
	for _, args := range [][]string{
		{"nope"}, {"context", "files", "a", "b"}, {"setup", "--agent"}, {"setup", "extra"}, {"source", "nope"},
		{"source", "add", "git"}, {"binding", "add"}, {"skill", "show"}, {"--config", "x.yaml", "setup"},
		{"source", "update", "--json"}, {"context", "show", "--part", "x"}, {"version", "x"}, {"help", "nope"},
	} {
		if code, _, _ := run(args...); code != 2 {
			t.Errorf("%v: exit %d, want 2", args, code)
		}
	}
	if _, _, e := run("source", "add", "git"); !strings.Contains(e, "ctxtra source add git: expected URL or DIR\nUsage: ctxtra source add git (URL | DIR) [flags]\n") {
		t.Errorf("usage error: %q", e)
	}
	binary = func() (string, []string, error) { return "/opt/homebrew/bin/ctxtra", nil, nil }
	t.Cleanup(func() { binary = setup.Binary })
	for _, args := range [][]string{{"setup", "-h"}, {"context", "files", "--help"}, {"help"}, {"help", "source", "add", "git"}, {"source"}, {}, {"--version"}} {
		if code, out, _ := run(args...); code != 0 || out == "" {
			t.Errorf("%v: exit %d, want 0 and output", args, code)
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
		_, out, _ := run("context", "explain", h)
		for _, a := range []string{"claude", "muse", "codex", "antigravity", "copilot", "pi", "hermes"} {
			warned := strings.Contains(out, ", "+a+" sessions don't get b.md\n")
			if warned != slices.Contains(tc.warn, a) {
				t.Errorf("%d bytes: %s warned %v:\n%s", len(tc.content), a, warned, out)
			}
		}
		if slices.Contains(tc.warn, "claude") && !strings.Contains(out, "warning: over Claude Code's limit of 360,000 characters, claude sessions don't get b.md\n") {
			t.Errorf("warning:\n%s", out)
		}
	}
}

// After setup, the next steps name the skills that no binding adds, and
// import while there are files to import and no binding adds instructions.
func TestSetupNextSteps(t *testing.T) {
	h := testenv.Home(t)
	binary = func() (string, []string, error) { return "/opt/homebrew/bin/ctxtra", nil, nil }
	t.Cleanup(func() { binary = setup.Binary })
	steps := func() string {
		t.Helper()
		code, out, e := run("setup", "--agent", "claude")
		if code != 0 {
			t.Fatalf("setup: %d %s", code, e)
		}
		_, next, _ := strings.Cut(out, "Next:\n")
		return next
	}
	if next := steps(); !strings.Contains(next, "ctxtra skill install ctxtra ") || !strings.Contains(next, "ctxtra skill install ctxtra-follow") ||
		strings.Contains(next, "ctxtra import") {
		t.Errorf("new config:\n%s", next)
	}
	testenv.Write(t, filepath.Join(h, ".claude", "CLAUDE.md"), "claude rules\n")
	if code, _, e := run("skill", "install", "ctxtra"); code != 0 {
		t.Fatalf("skill install: %s", e)
	}
	if next := steps(); strings.Contains(next, "ctxtra skill install ctxtra ") || !strings.Contains(next, "ctxtra import") {
		t.Errorf("skill installed, a file to import:\n%s", next)
	}
	testenv.Write(t, filepath.Join(h, "notes.md"), "mine\n")
	if code, _, e := run("binding", "add", "notes", "--global", "--instruction", filepath.Join(h, "notes.md")); code != 0 {
		t.Fatalf("binding add: %s", e)
	}
	if code, _, e := run("skill", "install", "ctxtra-follow"); code != 0 {
		t.Fatalf("skill install: %s", e)
	}
	if code, out, _ := run("setup", "--agent", "claude"); code != 0 || strings.Contains(out, "Next:") {
		t.Errorf("nothing left to do: %d\n%s", code, out)
	}
	if code, out, _ := run("setup", "--agent", "claude", "--dry-run"); code != 0 || strings.Contains(out, "Next:") {
		t.Errorf("dry run: %d\n%s", code, out)
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

// Only a whole line is an answer: input that ends before the end of a line
// accepts and dismisses nothing.
func TestAsk(t *testing.T) {
	for _, c := range []struct {
		in      string
		yes, ok bool
	}{
		{"y\n", true, true}, {"yes\n", true, true}, {"n\n", false, true}, {"\n", false, true},
		{"", false, false}, {"y", false, false}, {"n", false, false},
	} {
		var e bytes.Buffer
		s := &sources{IO: IO{Err: &e}, in: bufio.NewReader(strings.NewReader(c.in))}
		yes, err := s.ask("Accept? [y/N] ")
		if yes != c.yes || (err == nil) != c.ok {
			t.Errorf("%q: %v %v", c.in, yes, err)
		}
	}
}
