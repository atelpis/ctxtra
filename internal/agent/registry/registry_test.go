package registry

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/atelpis/ctxtra/internal/agent"
	"github.com/atelpis/ctxtra/internal/resolve"
	"github.com/atelpis/ctxtra/internal/testenv"
)

// Every agent has what the commands need, and answers with a JSON object
// also when there is nothing to add.
func TestAgentsAreComplete(t *testing.T) {
	h := testenv.Home(t)
	seen := map[string]bool{}
	for _, a := range All() {
		name := a.Name()
		if name == "" || seen[name] {
			t.Errorf("bad or duplicate name %q", name)
		}
		seen[name] = true
		if got, ok := Get(name); !ok || got.Name() != name {
			t.Errorf("Get(%q)", name)
		}
		if p := a.ConfigPath(); !filepath.IsAbs(p) || !strings.HasPrefix(p, h+"/") {
			t.Errorf("%s: config path %q", name, p)
		}
		for _, res := range []*resolve.Result{nil, {}} {
			b, err := json.Marshal(a.Answer("SessionStart", res))
			var m map[string]any
			if err != nil || json.Unmarshal(b, &m) != nil {
				t.Errorf("%s: answer %s (%v)", name, b, err)
			}
		}
		if st := a.Status("", false); !st.OK || st.State != "not set up" {
			t.Errorf("%s: status %+v", name, st)
		}
		inHome(t, h, a)
		var r agent.Report
		if err := a.Setup(agent.Options{Binary: "/bin/ctxtra", DryRun: true}, &r); err != nil || len(r.Changes) == 0 {
			t.Errorf("%s: dry run %v, %v", name, r.Changes, err)
		}
	}
	if _, ok := Get("vim"); ok {
		t.Error("unknown agent found")
	}
}

// With the binary gone, every hook command that setup writes prints a JSON
// object. The agents that can show the user a message show it once per
// session start, the others add nothing.
func TestMissingBinary(t *testing.T) {
	h := testenv.Home(t)
	bin := filepath.Join(h, "gone", "ctxtra")
	for _, a := range All() {
		inHome(t, h, a)
		var r agent.Report
		if err := a.Setup(agent.Options{Binary: bin}, &r); err != nil {
			t.Fatalf("%s: %v", a.Name(), err)
		}
		cmds := commands(t, a.ConfigPath())
		if strings.HasSuffix(a.ConfigPath(), ".json") && len(cmds) == 0 {
			t.Errorf("%s: no hook commands in %s", a.Name(), a.ConfigPath())
		}
		messages := 0
		for _, cmd := range cmds {
			c := exec.Command("/bin/sh", "-c", cmd)
			c.Env = []string{"HOME=" + h, "PATH=/missing"}
			c.Stdin = strings.NewReader(`{"cwd": "/x"}`)
			out, err := c.Output()
			var m map[string]string
			if err != nil || json.Unmarshal(out, &m) != nil {
				t.Errorf("%s: %v, %q from %s", a.Name(), err, out, cmd)
				continue
			}
			if msg, ok := m["systemMessage"]; ok {
				messages++
				if !strings.HasPrefix(msg, agent.MissingMessage(a.Name())) || len(m) != 1 || !strings.Contains(cmd, "SessionStart") {
					t.Errorf("%s: %q from %s", a.Name(), out, cmd)
				}
			} else if len(m) != 0 {
				t.Errorf("%s: %q from %s", a.Name(), out, cmd)
			}
		}
		if _, ok := a.(agent.Missing); ok && messages != 1 || !ok && messages != 0 {
			t.Errorf("%s: %d messages", a.Name(), messages)
		}
	}
}

// commands are the hook commands in a JSON hook file, none for another file.
func commands(t *testing.T, path string) []string {
	t.Helper()
	b, err := os.ReadFile(path)
	var v any
	if err != nil || json.Unmarshal(b, &v) != nil {
		return nil
	}
	var out []string
	var walk func(any)
	walk = func(v any) {
		switch v := v.(type) {
		case map[string]any:
			if cmd, ok := v["command"].(string); ok && strings.Contains(cmd, " hook ") {
				out = append(out, cmd)
			}
			for _, e := range v {
				walk(e)
			}
		case []any:
			for _, e := range v {
				walk(e)
			}
		}
	}
	walk(v)
	return out
}

// inHome stops the test before setup could write a file of a's outside the
// test's home folder h.
func inHome(t *testing.T, h string, a agent.Agent) {
	t.Helper()
	files := []string{a.ConfigPath()}
	if p, ok := a.(interface{ PluginPath() string }); ok {
		files = append(files, p.PluginPath())
	}
	for _, f := range files {
		if rel, err := filepath.Rel(h, f); err != nil || !filepath.IsLocal(rel) {
			t.Fatalf("%s: %s is outside the test home %s", a.Name(), f, h)
		}
	}
}

// Home clears every environment variable that the agents and ctxtra read,
// so no test changes the real user's files.
func TestHomeClearsAgentVars(t *testing.T) {
	re := regexp.MustCompile(`os\.(?:Getenv|LookupEnv)\("([A-Z0-9_]+)"\)`)
	files, _ := filepath.Glob("../*/*.go")
	more, _ := filepath.Glob("../../paths/*.go")
	found := 0
	for _, f := range append(files, more...) {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range re.FindAllStringSubmatch(string(b), -1) {
			found++
			if !slices.Contains(testenv.AgentVars, m[1]) {
				t.Errorf("%s reads %s, which testenv.Home doesn't clear", f, m[1])
			}
		}
	}
	if found < 10 {
		t.Errorf("found %d variables: the source files moved?", found)
	}
	for _, k := range testenv.AgentVars {
		t.Setenv(k, "/elsewhere")
	}
	h := testenv.Home(t)
	for _, a := range All() {
		inHome(t, h, a)
	}
}

// In an empty home, no agent has instruction files of its own, and every
// launch command ends with the prompt as one argument.
func TestNativeAndLaunch(t *testing.T) {
	testenv.Home(t)
	for _, a := range All() {
		if n, ok := a.(agent.Native); ok {
			if files := n.NativeFiles(); len(files) != 0 {
				t.Errorf("%s: files in an empty home: %+v", a.Name(), files)
			}
		}
		if l, ok := a.(agent.Launcher); ok {
			argv := l.Launch("a prompt")
			if len(argv) < 2 || argv[0] == "" || argv[len(argv)-1] != "a prompt" {
				t.Errorf("%s: launch %q", a.Name(), argv)
			}
		}
	}
}
