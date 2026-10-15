package cli

import (
	"bytes"
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
	testenv.Write(t, filepath.Join(h, ".config", "ctxtra", "b.yaml"), `apiVersion: ctxtra/v1alpha1
kind: Binding
metadata: {name: svc}
spec:
  selector: {gitRepos: [{remote: github.com/acme/svc}]}
  files: [~/a.md, ~/gone.md]
`)
	testenv.Config(t, h, "apiVersion: ctxtra/v1alpha1\nkind: Config\ninclude: [b.yaml]\n")

	if code, out, errOut := run("files", r); code != 1 || out != filepath.Join(h, "a.md")+"\n" || !strings.Contains(errOut, "missing: "+filepath.Join(h, "gone.md")) {
		t.Errorf("files: %d %q %q", code, out, errOut)
	}
	if code, out, _ := run("show", r); code != 0 || !strings.Contains(out, "Contents of "+filepath.Join(h, "a.md")) {
		t.Errorf("show: %d %q", code, out)
	}
	if _, out, _ := run("explain", h); !strings.Contains(out, "- svc (~/.config/ctxtra/b.yaml): gitRepos: not in a git repository") {
		t.Errorf("explain outside git:\n%s", out)
	}
	if _, out, _ := run("explain", r); !strings.Contains(out, "+ svc (~/.config/ctxtra/b.yaml): gitRepos remote github.com/acme/svc") ||
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
