package pi

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
	var r agent.Report
	if err := a.Setup(agent.Options{Binary: bin}, &r); err != nil {
		t.Fatal(err)
	}
	if st := a.Status(); !st.OK || st.State != "set up, runs ~/go/bin/ctxtra (ok)" {
		t.Errorf("status %+v", st)
	}
	if b, _ := os.ReadFile(a.ConfigPath()); !strings.Contains(string(b), `const bin = join(homedir(), "go/bin/ctxtra");`) {
		t.Errorf("binary path in the extension:\n%s", b)
	}
	if !strings.Contains(Extension("/opt/my tools/ctxtra"), `const bin = "/opt/my tools/ctxtra";`) {
		t.Error("absolute binary path in the extension")
	}
	if err := a.Setup(agent.Options{Remove: true}, &r); err != nil || a.Status().State != "not set up" {
		t.Fatalf("remove: %v, %+v", err, a.Status())
	}
	// A Pi extension that isn't ours is left alone.
	testenv.Write(t, a.ConfigPath(), "someone else's")
	if err := a.Setup(agent.Options{Binary: bin}, &r); err == nil {
		t.Error("foreign Pi extension: no error")
	}
	if b, _ := os.ReadFile(a.ConfigPath()); string(b) != "someone else's" {
		t.Errorf("Pi extension overwritten: %q", b)
	}
}

// Runs the extension in Node the way Pi calls it, with a binary that prints
// more than Node's default 1 MiB output buffer, and with no binary.
func TestExtensionRuns(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node not found")
	}
	dir := t.TempDir()
	bin := filepath.Join(dir, "ctxtra")
	const size = 2 << 20
	testenv.Write(t, bin, `#!/bin/sh
cat >/dev/null
printf '{"files":[{"path":"/a.md","content":"'
head -c `+itoa(size)+` /dev/zero | tr '\0' x
printf '"}],"skills":["/s/SKILL.md"],"message":"ctxtra: added 1 file"}'
`)
	if err := os.Chmod(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	testenv.Write(t, filepath.Join(dir, "main.mjs"), `
import ext from "./ctxtra.ts";
const on = {};
ext({ on: (ev, fn) => { on[ev] = fn; } });
const notes = [];
await on.session_start({}, { cwd: "/x", hasUI: true, ui: { notify: (m, type) => notes.push(type + ": " + m) } });
const discovered = await on.resources_discover({ cwd: "/x" });
const event = { systemPromptOptions: { contextFiles: [{ path: "/own.md", content: "own" }] } };
await on.before_agent_start(event, { cwd: "/x" });
const files = event.systemPromptOptions.contextFiles;
console.log(JSON.stringify({ skills: discovered.skillPaths, files: files.length, size: files.at(-1).content.length, notes }));
`)
	type result struct {
		Skills []string
		Files  int
		Size   int
		Notes  []string
	}
	run := func(bin string) result {
		t.Helper()
		testenv.Write(t, filepath.Join(dir, "ctxtra.ts"), Extension(bin))
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
	if r := run(bin); len(r.Skills) != 1 || r.Files != 2 || r.Size != size || len(r.Notes) != 1 || r.Notes[0] != "info: ctxtra: added 1 file" {
		t.Errorf("large output: %+v", r)
	}
	if r := run(filepath.Join(dir, "missing")); len(r.Skills) != 0 || r.Files != 1 || len(r.Notes) != 0 {
		t.Errorf("missing binary: %+v", r)
	}
}

func itoa(n int) string { b, _ := json.Marshal(n); return string(b) }
