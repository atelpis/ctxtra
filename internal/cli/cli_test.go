package cli

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
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
	if code, out, _ := run("hook", "claude"); code != 0 || out != "{}\n" {
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
	if code, out, _ := run("setup", "--agent", "claude", "--dry-run"); code != 0 || !strings.Contains(out, "would change") ||
		!strings.Contains(out, "hooks run /opt/homebrew/bin/ctxtra") {
		t.Errorf("setup dry run: %d %q", code, out)
	}
}

// Antigravity's limit is in bytes: the same number of characters warns for
// it when they take three bytes each, not when they take one.
func TestSizeWarnings(t *testing.T) {
	h := testenv.Home(t)
	testenv.Write(t, filepath.Join(h, ".config", "ctxtra", "b.yaml"),
		"schema: ctxtra/v1\nbindings:\n  - {name: me, match: {global: true}, instructions: [~/a.md]}\n")
	testenv.Config(t, h, "schema: ctxtra/v1\ninclude: [b.yaml]\n")
	for _, tc := range []struct {
		content     string
		antigravity bool
	}{
		{strings.Repeat("a", 17000), false},
		{strings.Repeat("界", 17000), true}, // 51,000 bytes
	} {
		testenv.Write(t, filepath.Join(h, "a.md"), tc.content)
		_, out, _ := run("explain", h)
		if !strings.Contains(out, "claude shows the model only a preview") ||
			strings.Contains(out, "antigravity drops the rest") != tc.antigravity {
			t.Errorf("%d bytes: warnings:\n%s", len(tc.content), out)
		}
	}
}

// In a terminal, setup offers the skill once; the answer decides.
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
	for _, answer := range []string{"n\n", "no\n", "x\n", ""} {
		if out := setupWith(answer); !strings.Contains(out, "Install it now? [Y/n]") || !strings.Contains(out, "not installed") {
			t.Errorf("declined with %q:\n%s", answer, out)
		}
	}
	if _, err := os.Stat(filepath.Join(h, ".ctxtra", "skills")); err == nil {
		t.Error("declined, but the skill was written")
	}
	if out := setupWith("\n"); !strings.Contains(out, "added binding ctxtra") {
		t.Errorf("accepted with Enter:\n%s", out)
	}
	if out := setupWith(""); strings.Contains(out, "ctxtra skill") {
		t.Errorf("installed, but asked again:\n%s", out)
	}
}
