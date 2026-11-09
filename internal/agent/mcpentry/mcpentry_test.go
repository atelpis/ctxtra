package mcpentry

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/ctxtra/ctxtra/internal/agent"
	"github.com/ctxtra/ctxtra/internal/jsonorder"
	"github.com/ctxtra/ctxtra/internal/testenv"
)

func claude(path string) Config {
	return Config{Agent: "claude", Path: path, Servers: []string{"mcpServers"}, Extra: []KV{{"type", "stdio"}}}
}

func opencode(path string) Config {
	return Config{Agent: "opencode", Path: path, Servers: []string{"mcp", "servers"}, CommandArray: true,
		Extra: []KV{{"type", "local"}, {"pool", false}}}
}

func apply(t *testing.T, c Config, o agent.MCPOptions) agent.Report {
	t.Helper()
	var r agent.Report
	if err := c.SetupMCP(o, &r); err != nil {
		t.Fatal(err)
	}
	return r
}

func read(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func add(bin string) agent.MCPOptions {
	return agent.MCPOptions{Options: agent.Options{Binary: bin}, Want: true}
}

func TestAddKeepsTheRest(t *testing.T) {
	h := testenv.Home(t)
	p := filepath.Join(h, ".claude.json")
	testenv.Write(t, p, `{"numStartups": 3, "mcpServers": {"linear": {"type": "http", "url": "https://x"}}, "z": true}`)
	c := claude(p)
	r := apply(t, c, add("/bin/ctxtra"))
	if len(r.Changes) != 1 {
		t.Fatalf("changes %v", r.Changes)
	}
	want := `{
  "numStartups": 3,
  "mcpServers": {
    "linear": {
      "type": "http",
      "url": "https://x"
    },
    "ctxtra": {
      "type": "stdio",
      "command": "/bin/ctxtra",
      "args": [
        "mcp",
        "proxy",
        "claude"
      ]
    }
  },
  "z": true
}
`
	if got := read(t, p); got != want {
		t.Errorf("got\n%s", got)
	}
	// Idempotent.
	if r := apply(t, c, add("/bin/ctxtra")); len(r.Changes) != 0 {
		t.Errorf("second run changed %v", r.Changes)
	}
	if st := c.MCPStatus("", agent.MCPCheck{Want: true, Known: true}); !strings.HasPrefix(st.State, "MCP entry runs /bin/ctxtra") {
		t.Errorf("status %+v", st)
	}
}

// A key the user added to ctxtra's entry survives setup; owned keys get
// setup's values.
func TestUserKeySurvives(t *testing.T) {
	h := testenv.Home(t)
	p := filepath.Join(h, "opencode.json")
	testenv.Write(t, p, `{"mcp": {"servers": {"ctxtra": {"type": "local", "command": ["/old/ctxtra", "mcp", "proxy", "opencode"], "pool": true, "timeout": 30000}}}}`)
	c := opencode(p)
	apply(t, c, add("/new/ctxtra"))
	got := read(t, p)
	for _, want := range []string{`"timeout": 30000`, `"pool": false`, `"/new/ctxtra",`} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %s in\n%s", want, got)
		}
	}
}

func TestRemove(t *testing.T) {
	h := testenv.Home(t)
	p := filepath.Join(h, "opencode.json")
	c := opencode(p)
	testenv.Write(t, p, `{"theme": "x"}`)
	apply(t, c, add("/bin/ctxtra"))
	apply(t, c, agent.MCPOptions{Options: agent.Options{Remove: true}})
	if got := read(t, p); got != "{\n  \"theme\": \"x\"\n}\n" {
		t.Errorf("empty objects on the way are removed too:\n%s", got)
	}
	// Nothing to remove: the file stays as it is.
	testenv.Write(t, p, `{"theme":"x"}`)
	if r := apply(t, c, agent.MCPOptions{Options: agent.Options{Remove: true}}); len(r.Changes) != 0 || read(t, p) != `{"theme":"x"}` {
		t.Errorf("changed %v", r.Changes)
	}
}

// Without MCP servers in the config, setup adds nothing and keeps an entry.
func TestNotWanted(t *testing.T) {
	h := testenv.Home(t)
	p := filepath.Join(h, ".claude.json")
	c := claude(p)
	if r := apply(t, c, agent.MCPOptions{Options: agent.Options{Binary: "/bin/ctxtra"}}); len(r.Changes) != 0 {
		t.Errorf("added %v", r.Changes)
	}
	if _, err := os.Stat(p); err == nil {
		t.Errorf("file created")
	}
	apply(t, c, add("/bin/ctxtra"))
	before := read(t, p)
	apply(t, c, agent.MCPOptions{Options: agent.Options{Binary: "/bin/ctxtra"}})
	if read(t, p) != before {
		t.Errorf("an existing entry stays")
	}
}

func TestDryRun(t *testing.T) {
	h := testenv.Home(t)
	p := filepath.Join(h, ".claude.json")
	o := add("/bin/ctxtra")
	o.DryRun = true
	if r := apply(t, claude(p), o); len(r.Changes) != 1 {
		t.Errorf("changes %v", r.Changes)
	}
	if _, err := os.Stat(p); err == nil {
		t.Errorf("dry run wrote the file")
	}
}

// A ctxtra entry that doesn't run the proxy is the user's: setup and
// --remove leave the file alone.
func TestChangedByHand(t *testing.T) {
	h := testenv.Home(t)
	p := filepath.Join(h, ".claude.json")
	for _, body := range []string{
		`{"mcpServers": {"ctxtra": {"command": "npx", "args": ["other"]}}}`,
		`{"mcpServers": {"ctxtra": "x"}}`,
	} {
		testenv.Write(t, p, body)
		c := claude(p)
		for _, o := range []agent.MCPOptions{add("/bin/ctxtra"), {Options: agent.Options{Remove: true}}} {
			var r agent.Report
			if err := c.SetupMCP(o, &r); err == nil || !strings.Contains(err.Error(), "doesn't run ctxtra mcp proxy; left alone") {
				t.Errorf("%s: %v", body, err)
			}
		}
		if read(t, p) != body {
			t.Errorf("changed")
		}
		if n := c.CheckMCP("", agent.MCPCheck{Want: true}); len(n) != 1 || !strings.Contains(n[0], "rename or delete it, then run ctxtra setup --agent claude") {
			t.Errorf("notices %v", n)
		}
	}
	testenv.Write(t, p, `{"mcpServers": []}`)
	var r agent.Report
	if err := claude(p).SetupMCP(add("/bin/ctxtra"), &r); err == nil || !strings.Contains(err.Error(), "mcpServers is not an object; left alone") {
		t.Errorf("%v", err)
	}
}

func TestCheck(t *testing.T) {
	h := testenv.Home(t)
	p := filepath.Join(h, ".claude.json")
	c := claude(p)
	bin := filepath.Join(h, "bin", "ctxtra")
	testenv.Write(t, bin, "#!/bin/sh\n")
	if err := os.Chmod(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		o    agent.MCPCheck
		want string
	}{
		{"no servers, never set up", agent.MCPCheck{}, ""},
		{"servers added after setup", agent.MCPCheck{Want: true}, "the ctxtra MCP server is missing; run ctxtra setup --agent claude"},
		{"gone", agent.MCPCheck{Known: true}, "the ctxtra MCP server is gone from ~/.claude.json; if that wasn't on purpose, run ctxtra setup --agent claude"},
	} {
		got := c.CheckMCP(bin, tc.o)
		if tc.want == "" && len(got) > 0 || tc.want != "" && (len(got) != 1 || !strings.HasPrefix(got[0], tc.want)) {
			t.Errorf("%s: %v", tc.name, got)
		}
	}
	apply(t, c, add("/elsewhere/ctxtra"))
	got := c.CheckMCP(bin, agent.MCPCheck{Want: true, Known: true})
	if len(got) != 1 || got[0] != "the ctxtra MCP server runs /elsewhere/ctxtra, which doesn't exist; run ctxtra setup --agent claude" {
		t.Errorf("another binary: %v", got)
	}
	if st := c.MCPStatus(bin, agent.MCPCheck{Want: true}); st.OK {
		t.Errorf("status %+v", st)
	}
	apply(t, c, add(bin))
	if got := c.CheckMCP(bin, agent.MCPCheck{Want: true, Known: true}); len(got) != 0 {
		t.Errorf("ok: %v", got)
	}
	if got := c.CheckMCP(filepath.Join(h, "other"), agent.MCPCheck{Want: true}); len(got) != 1 || !strings.Contains(got[0], "not this ctxtra") {
		t.Errorf("not this ctxtra: %v", got)
	}
	if !slices.Equal(c.commandLine(mustEntry(t, c)), []string{bin, "mcp", "proxy", "claude"}) {
		t.Errorf("command line")
	}
}

func mustEntry(t *testing.T, c Config) *jsonorder.Map {
	t.Helper()
	data, err := c.load()
	if err != nil {
		t.Fatal(err)
	}
	s, _ := c.servers(data, false)
	e, _ := entry(s)
	return e
}
