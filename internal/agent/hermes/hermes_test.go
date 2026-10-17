package hermes

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/atelpis/ctxtra/internal/agent"
	"github.com/atelpis/ctxtra/internal/testenv"
)

func TestSetup(t *testing.T) {
	h := testenv.Home(t)
	bin := filepath.Join(h, "go", "bin", "ctxtra")
	testenv.Write(t, bin, "#!/bin/sh\n")
	if err := os.Chmod(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	var a Agent
	plugin := filepath.Join(h, ".hermes", "plugins", "ctxtra")
	if a.ConfigPath() != filepath.Join(plugin, "__init__.py") {
		t.Errorf("config path %s", a.ConfigPath())
	}
	var r agent.Report
	if err := a.Setup(agent.Options{Binary: bin}, &r); err != nil {
		t.Fatal(err)
	}
	if len(r.Changes) != 2 || !slices.Contains(r.Notes, enableNote) {
		t.Errorf("setup report %+v", r)
	}
	if b, _ := os.ReadFile(a.ConfigPath()); !strings.Contains(string(b), `BIN = os.path.join(os.path.expanduser("~"), "go/bin/ctxtra")`) {
		t.Errorf("binary path in the plugin:\n%s", b)
	}
	if !strings.Contains(Plugin("/opt/my tools/ctxtra"), `BIN = "/opt/my tools/ctxtra"`) {
		t.Error("absolute binary path in the plugin")
	}
	// Set up but not enabled in Hermes.
	if st := a.Status(); st.OK || st.State != "set up, runs ~/go/bin/ctxtra (ok)" ||
		!slices.Contains(st.Details, "plugin NOT ENABLED in ~/.hermes/config.yaml (run hermes plugins enable ctxtra)") {
		t.Errorf("status %+v", st)
	}
	testenv.Write(t, filepath.Join(h, ".hermes", "config.yaml"), "model: x\nplugins:\n  enabled: [other, ctxtra]\n")
	if st := a.Status(); !st.OK || !slices.Contains(st.Details, "plugin enabled in ~/.hermes/config.yaml") {
		t.Errorf("enabled: %+v", st)
	}
	r = agent.Report{}
	if err := a.Setup(agent.Options{Binary: bin}, &r); err != nil || len(r.Changes) != 0 || len(r.Notes) != 0 {
		t.Errorf("setup again: %+v, %v", r, err)
	}
	testenv.Write(t, filepath.Join(h, ".hermes", "config.yaml"), "plugins:\n  enabled: [ctxtra]\n  disabled: [ctxtra]\n")
	if st := a.Status(); st.OK {
		t.Errorf("disabled: %+v", st)
	}
	testenv.Write(t, filepath.Join(h, ".hermes", "config.yaml"), "plugins: [\n")
	if st := a.Status(); st.OK || !strings.HasPrefix(st.Details[0], "plugin state unknown: ~/.hermes/config.yaml: ") {
		t.Errorf("broken config: %+v", st)
	}
	os.Remove(manifestPath())
	if st := a.Status(); st.OK || st.Details[0] != "~/.hermes/plugins/ctxtra/plugin.yaml: MISSING (run ctxtra setup)" {
		t.Errorf("no plugin.yaml: %+v", st)
	}

	// Remove deletes the folder, with the bytecode Python wrote.
	testenv.Write(t, filepath.Join(plugin, "__pycache__", "__init__.cpython-314.pyc"), "")
	if err := a.Setup(agent.Options{Remove: true}, &r); err != nil || a.Status().State != "not set up" {
		t.Fatalf("remove: %v, %+v", err, a.Status())
	}
	if _, err := os.Stat(plugin); !os.IsNotExist(err) {
		t.Errorf("plugin folder left: %v", err)
	}
	// A plugin.yaml that isn't ours: neither file is written.
	testenv.Write(t, manifestPath(), "name: ctxtra\n")
	if err := a.Setup(agent.Options{Binary: bin}, &r); err == nil {
		t.Error("foreign plugin.yaml: no error")
	}
	if _, err := os.Stat(a.ConfigPath()); !os.IsNotExist(err) {
		t.Errorf("__init__.py written next to a foreign plugin.yaml: %v", err)
	}
	if err := a.Setup(agent.Options{Remove: true}, &r); err == nil {
		t.Error("remove with a foreign plugin.yaml: no error")
	}
	if b, _ := os.ReadFile(manifestPath()); string(b) != "name: ctxtra\n" {
		t.Errorf("plugin.yaml overwritten: %q", b)
	}

	t.Setenv("HERMES_HOME", filepath.Join(h, "profiles", "work"))
	if want := filepath.Join(h, "profiles", "work", "plugins", "ctxtra", "__init__.py"); a.ConfigPath() != want {
		t.Errorf("with HERMES_HOME: %s", a.ConfigPath())
	}
}

// Runs the plugin in Python the way Hermes calls it, against a fake
// agent.runtime_cwd: a binary that prints a large answer, a later turn, a
// subagent, a session folder Hermes can't tell (nothing is added), and no
// binary.
func TestPluginRuns(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not found")
	}
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	calls := filepath.Join(dir, "calls")
	bin := filepath.Join(dir, "bin", "ctxtra")
	const size = 2 << 20
	testenv.Write(t, bin, `#!/bin/sh
printf '%s %s ' "$1 $2" "$3" >>`+calls+`
cat >>`+calls+`
echo >>`+calls+`
printf '{"context":"'
head -c `+itoa(size)+` /dev/zero | tr '\0' x
printf '"}'
`)
	if err := os.Chmod(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	testenv.Write(t, filepath.Join(dir, "agent", "__init__.py"), "")
	testenv.Write(t, filepath.Join(dir, "agent", "runtime_cwd.py"), `
import os
from pathlib import Path

def resolve_agent_cwd():
    if os.environ.get("NO_CWD"):
        raise RuntimeError("no session")
    return Path("/session")
`)
	testenv.Write(t, filepath.Join(dir, "main.py"), `
import importlib, json
plugin = importlib.import_module("ctxtra")
hooks = {}
class Ctx:
    def register_hook(self, name, fn):
        hooks[name] = fn
plugin.register(Ctx())
call = hooks["pre_llm_call"]
def size(r):
    return len(r["context"]) if r else 0
print(json.dumps({
    "first": size(call(session_id="s", is_first_turn=True, model="m", platform="cli", parent_session_id="")),
    "later": size(call(session_id="s", is_first_turn=False, model="m", platform="cli")),
    "subagent": size(call(session_id="c", is_first_turn=True, parent_session_id="s")),
}))
`)
	type result struct{ First, Later, Subagent int }
	run := func(bin string, env ...string) result {
		t.Helper()
		testenv.Write(t, filepath.Join(dir, "ctxtra", "__init__.py"), Plugin(bin))
		cmd := exec.Command("python3", "main.py")
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), env...)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("python: %v\n%s", err, out)
		}
		var r result
		if err := json.Unmarshal(out, &r); err != nil {
			t.Fatalf("output %q", out)
		}
		return r
	}
	if r := run(bin); r != (result{size, 0, size}) {
		t.Errorf("answers: %+v", r)
	}
	if r := run(bin, "NO_CWD=1"); r != (result{}) {
		t.Errorf("without a session folder: %+v", r)
	}
	if r := run(filepath.Join(dir, "missing")); r != (result{}) {
		t.Errorf("missing binary: %+v", r)
	}
	got, _ := os.ReadFile(calls)
	want := "hook hermes pre_llm_call {\"cwd\": \"/session\"}\nhook hermes subagent {\"cwd\": \"/session\"}\n"
	if string(got) != want {
		t.Errorf("calls:\n%s\nwant:\n%s", got, want)
	}
}

func itoa(n int) string { b, _ := json.Marshal(n); return string(b) }
