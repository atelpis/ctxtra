package dsh

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
	if want := filepath.Join(h, ".dsh", "cordis.patch.yml"); a.ConfigPath() != want {
		t.Errorf("config path %s", a.ConfigPath())
	}
	var r agent.Report
	if err := a.Setup(agent.Options{Binary: bin}, &r); err != nil {
		t.Fatal(err)
	}
	if len(r.Changes) != 2 {
		t.Errorf("changes %v", r.Changes)
	}
	if st := a.Status(); !st.OK || st.State != "set up, runs ~/go/bin/ctxtra (ok)" {
		t.Errorf("status %+v", st)
	}
	if b, _ := os.ReadFile(a.PluginPath()); !strings.Contains(string(b), `const bin = join(homedir(), "go/bin/ctxtra");`) {
		t.Errorf("binary path in the plugin:\n%s", b)
	}
	patch, _ := os.ReadFile(a.ConfigPath())
	if string(patch) != patchBlock(a.PluginPath()) {
		t.Errorf("patch:\n%s", patch)
	}
	if got, ok := patchPlugin(string(patch)); !ok || got != a.PluginPath() {
		t.Errorf("patch loads %q, %v:\n%s", got, ok, patch)
	}
	// Setup again changes nothing.
	r = agent.Report{}
	if err := a.Setup(agent.Options{Binary: bin}, &r); err != nil || len(r.Changes) != 0 {
		t.Errorf("second setup: %v, %v", err, r.Changes)
	}

	// Each half missing is a problem for doctor.
	if err := os.Remove(a.PluginPath()); err != nil {
		t.Fatal(err)
	}
	if st := a.Status(); st.OK || !strings.Contains(st.State, "loads a missing plugin") {
		t.Errorf("no plugin: %+v", st)
	}
	if err := a.Setup(agent.Options{Binary: bin}, &r); err != nil {
		t.Fatal(err)
	}
	testenv.Write(t, a.ConfigPath(), "[]\n")
	if st := a.Status(); st.OK || !strings.Contains(st.State, "doesn't load it") {
		t.Errorf("no row: %+v", st)
	}
	if err := a.Setup(agent.Options{Binary: bin}, &r); err != nil {
		t.Fatal(err)
	}

	r = agent.Report{}
	if err := a.Setup(agent.Options{Remove: true}, &r); err != nil || a.Status().State != "not set up" || len(r.Changes) != 2 {
		t.Fatalf("remove: %v, %+v, %v", err, a.Status(), r.Changes)
	}
	if b, _ := os.ReadFile(a.ConfigPath()); string(b) != "[]\n" {
		t.Errorf("patch after remove: %q", b)
	}
	// A plugin that isn't ours is left alone, and the patch isn't changed.
	testenv.Write(t, a.PluginPath(), "someone else's")
	if err := a.Setup(agent.Options{Binary: bin}, &r); err == nil {
		t.Error("foreign plugin: no error")
	}
	if b, _ := os.ReadFile(a.PluginPath()); string(b) != "someone else's" {
		t.Errorf("plugin overwritten: %q", b)
	}
	if b, _ := os.ReadFile(a.ConfigPath()); string(b) != "[]\n" {
		t.Errorf("patch changed: %q", b)
	}

	t.Setenv("DSH_HOME", "  ")
	if want := filepath.Join(h, ".dsh", "ctxtra.mjs"); a.PluginPath() != want {
		t.Errorf("with a blank DSH_HOME: %s", a.PluginPath())
	}
	t.Setenv("DSH_HOME", filepath.Join(h, "d"))
	if want := filepath.Join(h, "d", "cordis.patch.yml"); a.ConfigPath() != want {
		t.Errorf("with DSH_HOME: %s", a.ConfigPath())
	}
}

func TestEditPatch(t *testing.T) {
	block := patchBlock("/p/ctxtra.mjs")
	other := "# mine\n- id: llm-pi-ai\n  config:\n    when: !!js process.env.X\n"
	for _, c := range []struct{ name, old, want string }{
		{"no file", "", block},
		{"dsh's new file", "# Your patch layer\n[]\n", "# Your patch layer\n" + block},
		{"empty list with a comment", "[] # none yet\n", block},
		{"own entries", other, other + block},
		{"no final newline", strings.TrimSuffix(other, "\n"), other + block},
		{"already there", other + block, other + block},
		{"old binary in the middle", "- id: a\n" + patchBlock("/old.mjs") + "- id: b\n", "- id: a\n" + block + "- id: b\n"},
	} {
		got, err := editPatch(c.old, block)
		if err != nil || got != c.want {
			t.Errorf("%s: %v\n%s", c.name, err, got)
		}
	}
	for _, c := range []struct{ name, old, want string }{
		{"only ours", block, "[]\n"},
		{"with comments", "# mine\n" + block, "# mine\n[]\n"},
		{"own entries", other + block, other},
		{"not there", other, other},
		{"no file", "", ""},
	} {
		got, err := editPatch(c.old, "")
		if err != nil || got != c.want {
			t.Errorf("remove, %s: %v\n%s", c.name, err, got)
		}
	}
	for _, old := range []string{"[{id: a}]\n", "id: a\n", "- [\n", beginMark + "\n- id: a\n"} {
		if got, err := editPatch(old, block); err == nil {
			t.Errorf("%q: no error:\n%s", old, got)
		}
	}
}

// Runs the plugin in Node against a fake DSH: a binary that prints more than
// Node's default 1 MiB output buffer, a main agent, a subagent, a resumed
// session, DSH with and without the workingDirectory service, and no binary.
func TestPluginRuns(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node not found")
	}
	dir := t.TempDir()
	bin := filepath.Join(dir, "ctxtra")
	const size = 2 << 20
	testenv.Write(t, bin, `#!/bin/sh
echo "$1 $2 $3 $(cat)" >> "$0.calls"
printf '{"text":"'
head -c `+itoa(size)+` /dev/zero | tr '\0' x
printf '"}'
`)
	if err := os.Chmod(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	// Each agent's steps: a step without messages and a rejected one keep the
	// text; the first step with messages gets it last; later steps don't.
	testenv.Write(t, filepath.Join(dir, "main.mjs"), `
import * as plugin from "./ctxtra.mjs";
const run = async (wd) => {
	const on = {};
	plugin.apply({ on: (name, fn) => { on[name] = fn; }, get: (name) => name === "workingDirectory" ? wd : undefined });
	const added = [];
	for (const [source, header] of [["startup", { cwd: "/a" }], ["startup", { cwd: "/a", parentSession: "s1" }], ["resume", { cwd: "/a" }], ["compact", { cwd: "/b" }]]) {
		const agent = { session: { header } };
		await on["agent/created"]({ agent, source });
		const steps = [];
		for (const decision of [{ kind: "enter", messages: [] }, { kind: "reject" }, { kind: "enter", messages: ["prompt", "AGENTS.md"] }, { kind: "enter", messages: ["tool"] }]) {
			const out = await on["agent/pre-step"]({ agent, messages: [] }, async () => decision);
			const extra = out.messages?.slice(decision.messages.length) ?? [];
			steps.push(extra.map((m) => m.role + ":" + m.source.kind + ":" + m.content[0].text.length + "@" + out.messages.indexOf(m)).join("+"));
		}
		added.push(steps.join(" "));
	}
	return added;
};
const withService = await run({ ensure: async () => "/wd" });
const failing = await run({ ensure: async () => { throw new Error("gone"); } });
const without = await run(undefined);
console.log(JSON.stringify({ name: plugin.name, withService, failing, without }));
`)
	type result struct {
		Name                          string
		WithService, Failing, Without []string
	}
	run := func(bin string) result {
		t.Helper()
		testenv.Write(t, filepath.Join(dir, "ctxtra.mjs"), Plugin(bin))
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
	added := "  user:ctxtra:" + itoa(size) + "@2 "
	for _, steps := range [][]string{r.WithService, r.Failing, r.Without} {
		if strings.Join(steps, "|") != added+"|"+added+"|   |"+added {
			t.Errorf("large output: %q", steps)
		}
	}
	if r.Name != "ctxtra" {
		t.Errorf("plugin name %q", r.Name)
	}
	// Nothing for resume; the subagent's run is logged as one.
	calls, _ := os.ReadFile(bin + ".calls")
	want := "hook dsh startup {\"cwd\":\"/wd\"}\nhook dsh subagent {\"cwd\":\"/wd\"}\nhook dsh compact {\"cwd\":\"/wd\"}\n" +
		"hook dsh startup {\"cwd\":\"/a\"}\nhook dsh subagent {\"cwd\":\"/a\"}\nhook dsh compact {\"cwd\":\"/b\"}\n" +
		"hook dsh startup {\"cwd\":\"/a\"}\nhook dsh subagent {\"cwd\":\"/a\"}\nhook dsh compact {\"cwd\":\"/b\"}\n"
	if string(calls) != want {
		t.Errorf("calls:\n%s", calls)
	}
	if r := run(filepath.Join(dir, "missing")); strings.Join(r.Without, "|") != "   |   |   |   " {
		t.Errorf("missing binary: %+v", r)
	}
}

func itoa(n int) string { b, _ := json.Marshal(n); return string(b) }
