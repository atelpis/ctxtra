package agent_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/atelpis/ctxtra/internal/agent"
	"github.com/atelpis/ctxtra/internal/testenv"
)

// The hook text must stay byte-identical across versions: Codex trust is a
// hash of it.
func TestCommandIsStable(t *testing.T) {
	h := testenv.Home(t)
	for bin, want := range map[string]string{
		filepath.Join(h, "go", "bin", "ctxtra"): `/bin/sh -c 'f="$HOME/go/bin/ctxtra"; if [ -x "$f" ]; then exec "$f" hook codex SessionStart; else /bin/cat >/dev/null 2>&1; echo "{}"; fi'`,
		"/opt/homebrew/bin/ctxtra":              `/bin/sh -c 'f="/opt/homebrew/bin/ctxtra"; if [ -x "$f" ]; then exec "$f" hook codex SessionStart; else /bin/cat >/dev/null 2>&1; echo "{}"; fi'`,
	} {
		got := agent.Command(bin, "codex", "SessionStart")
		if got != want {
			t.Errorf("hook command changed:\n got %s\nwant %s", got, want)
		}
		if b, ok := agent.CommandBinary(got); !ok || b != bin {
			t.Errorf("CommandBinary(%s) = %q, %v", got, b, ok)
		}
	}
	if _, ok := agent.CommandBinary("other-tool hook codex SessionStart"); ok {
		t.Error("another tool's command recognized as ours")
	}
	for _, bad := range []string{"rel/ctxtra", `/a"b`, "/a$b", "/a'b", "/a`b", `/a\b`} {
		if agent.ValidBinaryPath(bad) == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

// Runs the command the way the agents do: through the shell, with the hook
// JSON on stdin, from a folder elsewhere and with a minimal PATH.
func TestCommandRuns(t *testing.T) {
	h := testenv.Home(t)
	bin := filepath.Join(h, "my tools", "ctxtra")
	testenv.Write(t, bin, "#!/bin/sh\necho \"args: $*\"\ncat\n")
	if err := os.Chmod(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	run := func() string {
		t.Helper()
		cmd := exec.Command("/bin/sh", "-c", agent.Command(bin, "claude", "SessionStart"))
		cmd.Env = []string{"HOME=" + h, "PATH=/usr/bin:/bin"}
		cmd.Dir = t.TempDir()
		cmd.Stdin = strings.NewReader(`{"cwd":"/x"}`)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("%v: %s", err, out)
		}
		return string(out)
	}
	if got := run(); got != "args: hook claude SessionStart\n{\"cwd\":\"/x\"}" {
		t.Errorf("output %q", got)
	}
	// With the binary gone the hook adds nothing and still reads all of
	// stdin, also with no system tools in PATH: the agent must not get a
	// broken pipe.
	if err := os.Remove(bin); err != nil {
		t.Fatal(err)
	}
	if got := run(); got != "{}\n" {
		t.Errorf("missing binary: %q", got)
	}
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("/bin/sh", "-c", agent.Command(bin, "claude", "SessionStart"))
	cmd.Env = []string{"HOME=" + h, "PATH=/missing"}
	cmd.Stdin = r
	var out strings.Builder
	cmd.Stdout = &out
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	r.Close()
	_, werr := w.Write(make([]byte, 2<<20))
	w.Close()
	if err := cmd.Wait(); err != nil || werr != nil || out.String() != "{}\n" {
		t.Errorf("large input, empty PATH: write %v, exit %v, output %q", werr, err, out.String())
	}
}
