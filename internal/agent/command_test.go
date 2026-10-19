package agent_test

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/atelpis/ctxtra/internal/agent"
	"github.com/atelpis/ctxtra/internal/testenv"
)

// message is an agent whose hook answer shows the user a message.
type message struct{}

func (message) MissingAnswer(event, msg string) string { return agent.SystemMessage(event, msg) }

// The hook text must stay byte-identical across versions: Codex trust is a
// hash of it.
func TestCommandIsStable(t *testing.T) {
	h := testenv.Home(t)
	for bin, want := range map[string]string{
		filepath.Join(h, "go", "bin", "ctxtra"): `/bin/sh -c 'f="$HOME/go/bin/ctxtra"; if [ -x "$f" ]; then exec "$f" hook codex SessionStart; else /bin/cat >/dev/null 2>&1; echo "{}"; fi'`,
		"/opt/homebrew/bin/ctxtra":              `/bin/sh -c 'f="/opt/homebrew/bin/ctxtra"; if [ -x "$f" ]; then exec "$f" hook codex SessionStart; else /bin/cat >/dev/null 2>&1; echo "{}"; fi'`,
	} {
		got := agent.Command(bin, "codex", "SessionStart", nil)
		if got != want {
			t.Errorf("hook command changed:\n got %s\nwant %s", got, want)
		}
		if c, ok := agent.ParseCommand(got, nil); !ok || c != (agent.Hook{Binary: bin, Agent: "codex", Event: "SessionStart", Slot: -1}) {
			t.Errorf("ParseCommand(%s) = %+v, %v", got, c, ok)
		}
	}
	withMessage := agent.Command("/opt/ctxtra", "devin", "SessionStart", message{})
	if want := `/bin/sh -c 'f="/opt/ctxtra"; if [ -x "$f" ]; then exec "$f" hook devin SessionStart; else /bin/cat >/dev/null 2>&1; ` +
		`echo "{\"systemMessage\":\"ctxtra: the ctxtra binary that this hook runs is missing; reinstall ctxtra, or run ctxtra setup --agent devin with the new binary\"}"; fi'`; withMessage != want {
		t.Errorf("command with a message:\n got %s\nwant %s", withMessage, want)
	}
	slot := agent.SlotCommand("/opt/ctxtra", "claude", "SessionStart", 17, 40, message{})
	if want := `/bin/sh -c 'f="/opt/ctxtra"; if [ -x "$f" ]; then exec "$f" hook claude SessionStart --slot 17 --of 40; else /bin/cat >/dev/null 2>&1; echo "{}"; fi'`; slot != want {
		t.Errorf("slot command:\n got %s\nwant %s", slot, want)
	}
	if c, ok := agent.ParseCommand(slot, message{}); !ok || c.Slot != 17 || c.Slots != 40 || c.Binary != "/opt/ctxtra" {
		t.Errorf("ParseCommand of a slot command: %+v, %v", c, ok)
	}
	if c, _ := agent.ParseCommand(agent.Command("/opt/ctxtra", "claude", "SessionStart", nil), nil); c.Slot != -1 || c.Slots != 0 {
		t.Errorf("a slot in a command without one: %+v", c)
	}
	if _, ok := agent.ParseCommand("other-tool hook codex SessionStart", nil); ok {
		t.Error("another tool's command recognized as ours")
	}
	for _, bad := range []string{"rel/ctxtra", `/a"b`, "/a$b", "/a'b", "/a`b", `/a\b`} {
		if agent.ValidBinaryPath(bad) == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

// Setup recognizes the forms it writes and wrote, with the answers for a
// missing binary that it writes: the message only in slot 0, or in a hook
// without slots, at session start. A command edited by hand isn't ours.
func TestParseCommand(t *testing.T) {
	const pre = `/bin/sh -c 'f="/opt/ctxtra"; if [ -x "$f" ]; then exec "$f" hook claude `
	msg := strings.ReplaceAll(agent.SystemMessage("SessionStart", agent.MissingMessage("claude")), `"`, `\"`)
	for cmd, want := range map[string]bool{
		agent.SlotCommand("/opt/ctxtra", "claude", "SessionStart", 0, 40, message{}): true,
		agent.SlotCommand("/opt/ctxtra", "claude", "SessionStart", 1, 40, message{}): true,
		agent.Command("/opt/ctxtra", "claude", "SessionStart", message{}):            true,
		agent.Command("/opt/ctxtra", "claude", "SubagentStart", message{}):           true,
		// Earlier versions: {} with /bin/cat or cat, slots without --of.
		pre + `SessionStart --slot 0 --of 40; else /bin/cat >/dev/null 2>&1; echo "{}"; fi'`: true,
		pre + `SessionStart --slot 3; else /bin/cat >/dev/null 2>&1; echo "{}"; fi'`:         true,
		pre + `SessionStart; else cat >/dev/null 2>&1; echo "{}"; fi'`:                       true,
		// The message where setup doesn't write it, or edited.
		pre + `SessionStart --slot 1 --of 40; else /bin/cat >/dev/null 2>&1; echo "` + msg + `"; fi'`: false,
		pre + `SubagentStart; else /bin/cat >/dev/null 2>&1; echo "` + msg + `"; fi'`:                 false,
		pre + `SessionStart; else /bin/cat >/dev/null 2>&1; echo "{\"systemMessage\":\"gone\"}"; fi'`: false,
		pre + `SessionStart; else /bin/cat >/dev/null 2>&1; echo "{}" >&2; fi'`:                       false,
		pre + `SessionStart; else cat >/dev/null 2>&1; echo "` + msg + `"; fi'`:                       false,
		pre + `SessionStart --slot x; else /bin/cat >/dev/null 2>&1; echo "{}"; fi'`:                  false,
	} {
		if _, ok := agent.ParseCommand(cmd, message{}); ok != want {
			t.Errorf("ParseCommand(%s) = %v, want %v", cmd, ok, want)
		}
	}
	// Without a message, only {} is ours.
	if _, ok := agent.ParseCommand(agent.Command("/opt/ctxtra", "claude", "SessionStart", message{}), nil); ok {
		t.Error("a message recognized for an agent without one")
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
		cmd := exec.Command("/bin/sh", "-c", agent.Command(bin, "claude", "SessionStart", nil))
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
	cmd := exec.Command("/bin/sh", "-c", agent.Command(bin, "claude", "SessionStart", message{}))
	cmd.Env = []string{"HOME=" + h, "PATH=/missing"}
	cmd.Stdin = strings.NewReader(`{"cwd":"/x"}`)
	var m map[string]string
	if out, err := cmd.Output(); err != nil || json.Unmarshal(out, &m) != nil || m["systemMessage"] != agent.MissingMessage("claude") || len(m) != 1 {
		t.Errorf("missing binary, with a message: %v %q", err, out)
	}
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	cmd = exec.Command("/bin/sh", "-c", agent.Command(bin, "claude", "SessionStart", nil))
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
