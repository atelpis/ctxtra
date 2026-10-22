package opencode

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

func TestSetup(t *testing.T) {
	h := testenv.Home(t)
	bin := filepath.Join(h, "go", "bin", "ctxtra")
	testenv.Write(t, bin, "#!/bin/sh\n")
	if err := os.Chmod(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	var a Agent
	if want := filepath.Join(h, ".config", "opencode", "plugins", "ctxtra.ts"); a.ConfigPath() != want {
		t.Errorf("config path %s", a.ConfigPath())
	}
	var r agent.Report
	if err := a.Setup(agent.Options{Binary: bin}, &r); err != nil {
		t.Fatal(err)
	}
	if st := a.Status("", false); !st.OK || st.State != "set up, runs ~/go/bin/ctxtra (ok)" {
		t.Errorf("status %+v", st)
	}
	if b, _ := os.ReadFile(a.ConfigPath()); !strings.Contains(string(b), `const bin = join(homedir(), "go/bin/ctxtra");`) {
		t.Errorf("binary path in the plugin:\n%s", b)
	}
	if err := a.Setup(agent.Options{Remove: true}, &r); err != nil || a.Status("", false).State != "not set up" {
		t.Fatalf("remove: %v, %+v", err, a.Status("", false))
	}
	// A plugin that isn't ours is left alone.
	testenv.Write(t, a.ConfigPath(), "someone else's")
	if err := a.Setup(agent.Options{Binary: bin}, &r); err == nil {
		t.Error("foreign plugin: no error")
	}
	if b, _ := os.ReadFile(a.ConfigPath()); string(b) != "someone else's" {
		t.Errorf("plugin overwritten: %q", b)
	}

	t.Setenv("XDG_CONFIG_HOME", filepath.Join(h, "xdg"))
	if want := filepath.Join(h, "xdg", "opencode", "plugins", "ctxtra.ts"); a.ConfigPath() != want {
		t.Errorf("with XDG_CONFIG_HOME: %s", a.ConfigPath())
	}
	t.Setenv("OPENCODE_CONFIG_DIR", filepath.Join(h, "oc"))
	if want := filepath.Join(h, "oc", "plugins", "ctxtra.ts"); a.ConfigPath() != want {
		t.Errorf("with OPENCODE_CONFIG_DIR: %s", a.ConfigPath())
	}
}

// Runs the plugin in Node against a fake OpenCode: a binary that prints more
// than Node's default 1 MiB output buffer, a user skill with the same name
// as a bound one, two sessions in different folders, a skill list that
// changes, and no binary.
func TestPluginRuns(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node not found")
	}
	dir := t.TempDir()
	bin := filepath.Join(dir, "ctxtra")
	const size = 2 << 20
	// The answer depends on the folder: /b gets a second skill.
	testenv.Write(t, bin, `#!/bin/sh
in=$(cat)
echo "$1 $2 $3 $in" >> "$0.calls"
extra=''
case "$in" in *'"/b"'*) extra=',{"name":"lint","description":"L","path":"/l/SKILL.md","content":"LINT"}';; esac
printf '{"text":"'
head -c `+itoa(size)+` /dev/zero | tr '\0' x
printf '","skills":[{"name":"deploy","description":"D","path":"/d/SKILL.md","content":"BODY"},{"name":"mine","description":"M","path":"/m/SKILL.md","content":"M"}'"$extra"']}'
`)
	if err := os.Chmod(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	testenv.Write(t, filepath.Join(dir, "main.mjs"), `
import plugin from "./ctxtra.ts";
let transform, hook, reloads = 0;
const user = { id: "mine", name: "mine", content: "USER" };
const skills = () => { const list = [user]; transform({ get: (id) => list.find((s) => s.id === id), add: (s) => list.push(s) }); return list; };
const dirs = { s1: "/a", s2: "/b" };
await plugin.setup({
	location: { directory: "/a" },
	skill: { transform: async (fn) => { transform = fn; }, reload: async () => { reloads++; } },
	session: {
		hook: async (name, fn) => { if (name === "context") hook = fn; },
		get: async ({ sessionID }) => ({ id: sessionID, location: { directory: dirs[sessionID] } }),
	},
});
const before = skills();
const sizes = [];
for (const id of ["s1", "s1", "s2"]) {
	const event = { sessionID: id, system: [{ type: "text", text: "own" }] };
	await hook(event);
	sizes.push(event.system.length === 2 ? event.system[1].text.length : 0);
}
console.log(JSON.stringify({ plugin: plugin.id, before: before.map((s) => s.id + ":" + s.content), after: skills().map((s) => s.id), sizes, reloads }));
`)
	type result struct {
		Plugin  string
		Before  []string
		After   []string
		Sizes   []int
		Reloads int
	}
	run := func(bin string) result {
		t.Helper()
		testenv.Write(t, filepath.Join(dir, "ctxtra.ts"), Plugin(bin))
		cmd := exec.Command("node", "main.mjs")
		cmd.Dir = dir
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("node: %v\n%s", err, out)
		}
		var r result
		if err := json.Unmarshal(out, &r); err != nil {
			t.Fatalf("output %q", out)
		}
		return r
	}
	r := run(bin)
	if r.Plugin != "ctxtra" || strings.Join(r.Before, ",") != "mine:USER,deploy:BODY" || strings.Join(r.After, ",") != "mine,deploy,lint" ||
		len(r.Sizes) != 3 || r.Sizes[0] != size || r.Sizes[1] != size || r.Sizes[2] != size || r.Reloads != 1 {
		t.Errorf("large output: %+v", r)
	}
	// One run at setup, then one per session, not one per request.
	calls, _ := os.ReadFile(bin + ".calls")
	if want := "hook opencode setup {\"cwd\":\"/a\",\"ctxtra_plugin\":1}\nhook opencode context {\"cwd\":\"/a\",\"ctxtra_plugin\":1}\nhook opencode context {\"cwd\":\"/b\",\"ctxtra_plugin\":1}\n"; string(calls) != want {
		t.Errorf("calls:\n%s", calls)
	}
	if r := run(filepath.Join(dir, "missing")); strings.Join(r.Before, ",") != "mine:USER" || len(r.Sizes) != 3 || r.Sizes[0] != 0 || r.Reloads != 0 {
		t.Errorf("missing binary: %+v", r)
	}
}

func itoa(n int) string { b, _ := json.Marshal(n); return string(b) }
