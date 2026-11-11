package e2e

import (
	"bufio"
	"encoding/json"
	"os"
	"os/exec"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/ctxtra/ctxtra/e2e/driver"
	"github.com/ctxtra/ctxtra/e2e/mock"
)

// mcpAgents are the agents that ctxtra setup gives its MCP server, and
// that the proxy is checked in. Not Muse: it starts MCP servers with a
// cleared environment.
var mcpAgents = []string{"claude", "codex", "pi", "opencode", "antigravity"}

// mcpSession is a session with MCP servers on: the config is at a path of
// its own ($CTXTRA_CONFIG), so a proxy that serves tools read the same
// config as the hook. The agent approves the ctxtra server's tools.
func mcpSession(t *testing.T, a Agent, config func(s *Session) string) *Session {
	t.Helper()
	if !slices.Contains(mcpAgents, a.Name()) {
		t.Skip("no MCP entry for " + a.Name())
	}
	s := newSession(t, a)
	cfg := s.Path("ctx/ctxtra.yaml")
	s.Extra = append(s.Extra, "CTXTRA_EXPERIMENTAL_MCP=1", "CTXTRA_CONFIG="+cfg)
	s.Write("ctx/ctxtra.yaml", config(s))
	s.Setup()
	s.Adjust = func(cmd *exec.Cmd) { approveMCP(s, a.Name(), cmd) }
	return s
}

// approveMCP lets a headless run call the ctxtra server's tools.
func approveMCP(s *Session, agent string, cmd *exec.Cmd) {
	args := cmd.Args[1:]
	switch agent {
	case "claude":
		// Every MCP tool in the first request, not behind tool search.
		cmd.Env = append(cmd.Env, "ENABLE_TOOL_SEARCH=false")
		for i, a := range args {
			if a == "--allowedTools" {
				args[i+1] += ",mcp__ctxtra"
			}
		}
	case "codex":
		// exec refuses MCP calls without it.
		args = append([]string{args[0], "-c", `mcp_servers.ctxtra.default_tools_approval_mode="approve"`}, args[1:]...)
	case "muse":
		args = append(append(slices.Clone(args[:len(args)-1]), "--disable-approval"), args[len(args)-1])
	case "opencode":
		// The driver writes OpenCode's config for each run, without
		// ctxtra's entry: setup adds it again, as a user's would be kept.
		s.Ctxtra("setup", "--agent", "opencode")
	case "antigravity":
		// Headless agy denies MCP calls without an allow rule.
		p := s.Path(".gemini/antigravity-cli/settings.json")
		var st map[string]any
		if b, err := os.ReadFile(p); err == nil && json.Unmarshal(b, &st) == nil {
			pm, _ := st["permissions"].(map[string]any)
			if pm == nil {
				pm = map[string]any{}
				st["permissions"] = pm
			}
			allow, _ := pm["allow"].([]any)
			pm["allow"] = append(allow, "mcp(ctxtra/*)")
			b, _ = json.MarshalIndent(st, "", "  ")
			os.WriteFile(p, b, 0o644)
		}
	}
	cmd.Args = append(cmd.Args[:1:1], args...)
}

// mcpScript calls the ctxtra server's tools the way the agent offers
// them. OpenCode connects MCP servers in the background, about a second
// after its start, and the mock answers at once: its first turn waits two
// seconds in a script.
func mcpScript(agent string, names ...string) []mock.Step {
	if agent == "opencode" {
		wait := mock.CallCode("execute", "const t = Date.now(); while (Date.now() - t < 2000) {} return 1")
		return []mock.Step{wait, mcpCalls(agent, names...)}
	}
	return []mock.Step{mcpCalls(agent, names...)}
}

// mcpCalls is the script step that calls the ctxtra server's tools the
// way the agent offers them: names are the names sessions see.
func mcpCalls(agent string, names ...string) mock.Step {
	switch agent {
	case "pi":
		// Pi's default exposure: MCP tools are methods of tools in its
		// codemode script, as mcp__<server>__<tool>.
		var code strings.Builder
		for _, n := range names {
			code.WriteString("await tools.mcp__ctxtra__" + n + "({note: \"probe\"});\n")
		}
		code.WriteString("return \"done\"")
		return mock.CallCode("codemode", code.String())
	case "muse":
		return mock.CallMCPToolsFlat(names...)
	case "antigravity":
		return mock.CallMCPGeneric("call_mcp_tool", "ctxtra", names...)
	case "opencode":
		return mock.CallMCPCode("execute", "ctxtra", names...)
	}
	return mock.CallMCPTools(names...)
}

// shownTools are the names sessions see for server, from ctxtra mcp show.
func shownTools(t *testing.T, s *Session, server string) map[string]string {
	t.Helper()
	out := s.Ctxtra("mcp", "show", server)
	tools := map[string]string{}
	re := regexp.MustCompile(`^  (\S+)(?: \(shortened from (\S+)\))?$`)
	for _, line := range strings.Split(out, "\n") {
		if m := re.FindStringSubmatch(line); m != nil {
			orig := m[2]
			if orig == "" {
				orig = strings.TrimPrefix(m[1], server+"__")
			}
			tools[orig] = m[1]
		}
	}
	return tools
}

// probeCalls are the tool names that the probe logged in file got calls
// for, and how often it was started.
func probeCalls(t *testing.T, file string) (calls []string, starts int) {
	t.Helper()
	f, err := os.Open(file)
	if os.IsNotExist(err) {
		return nil, 0
	}
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<24)
	for sc.Scan() {
		var rec struct {
			Event, Method string
			Params        struct{ Name string }
		}
		if json.Unmarshal(sc.Bytes(), &rec) != nil || rec.Event != "client_message" {
			continue
		}
		switch rec.Method {
		case "initialize":
			starts++
		case "tools/call":
			calls = append(calls, rec.Params.Name)
		}
	}
	return calls, starts
}

// The proxy in each agent: setup adds the entry; a session in a bound
// folder gets that folder's server and calls it, also through a shortened
// name, which reaches the server as it lists it; another folder gets its
// own server; an unbound folder starts none. The proxy reads the config
// the hook reads ($CTXTRA_CONFIG).
func TestMCPProxy(t *testing.T) {
	forEachAgent(t, nil, func(t *testing.T, a Agent) {
		probe := probeBinary(t)
		s := mcpSession(t, a, func(s *Session) string {
			return `schema: ctxtra/v1
mcp-servers:
  alpha: {command: [` + probe + `, mcp, -log, ` + s.Path("alpha.jsonl") + `]}
  beta: {command: [` + probe + `, mcp, -log, ` + s.Path("beta.jsonl") + `]}
bindings:
  - name: a
    match: {dirs: [~/dev/a]}
    mcp-servers: [alpha]
  - name: b
    match: {dirs: [~/dev/b]}
    mcp-servers: [beta]
`
		})
		alphaLog, betaLog := s.Path("alpha.jsonl"), s.Path("beta.jsonl")
		for _, d := range []string{"dev/a", "dev/b", "dev/c"} {
			s.Write(d+"/README.md", "x")
		}
		alpha := shownTools(t, s, "alpha")
		long := alpha["linear-work__list_issues"]
		if long == "" || !strings.Contains(long, "_list_issues_") {
			t.Fatalf("alpha's tools: %v", alpha)
		}
		// The probe's own runs from mcp show don't count.
		os.Remove(alphaLog)

		s.Run(s.Path("dev/a"), driver.Options{}, mcpScript(a.Name(), alpha["probe_ping"], long)...)
		calls, _ := probeCalls(t, alphaLog)
		if !slices.Contains(calls, "probe_ping") || !slices.Contains(calls, "linear-work__list_issues") {
			t.Errorf("alpha got calls %v, want probe_ping and linear-work__list_issues", calls)
		}
		if _, starts := probeCalls(t, betaLog); starts > 0 {
			t.Errorf("beta started %d times for a session in dev/a", starts)
		}

		_, alphaStarts := probeCalls(t, alphaLog)
		s.Run(s.Path("dev/b"), driver.Options{}, mcpScript(a.Name(), "beta__probe_ping")...)
		if calls, _ := probeCalls(t, betaLog); !slices.Contains(calls, "probe_ping") {
			t.Errorf("beta got calls %v in dev/b", calls)
		}
		if _, n := probeCalls(t, alphaLog); n != alphaStarts {
			t.Errorf("alpha started for a session in dev/b")
		}

		_, betaStarts := probeCalls(t, betaLog)
		s.Run(s.Path("dev/c"), driver.Options{})
		if _, n := probeCalls(t, alphaLog); n != alphaStarts {
			t.Errorf("alpha started for a session in dev/c")
		}
		if _, n := probeCalls(t, betaLog); n != betaStarts {
			t.Errorf("beta started for a session in dev/c")
		}
	})
}

// A bound server that fails to start costs only its own tools: the
// session starts, and the other server's tools work.
func TestMCPFailingServer(t *testing.T) {
	forEachAgent(t, nil, func(t *testing.T, a Agent) {
		probe := probeBinary(t)
		s := mcpSession(t, a, func(s *Session) string {
			return `schema: ctxtra/v1
mcp-servers:
  alpha: {command: [` + probe + `, mcp, -log, ` + s.Path("alpha.jsonl") + `]}
  broken: {command: [/nonexistent/mcp-server]}
bindings:
  - name: a
    match: {dirs: [~/dev/a]}
    mcp-servers: [broken, alpha]
`
		})
		s.Write("dev/a/README.md", "x")
		s.Run(s.Path("dev/a"), driver.Options{}, mcpScript(a.Name(), "alpha__probe_ping")...)
		if calls, _ := probeCalls(t, s.Path("alpha.jsonl")); !slices.Contains(calls, "probe_ping") {
			t.Errorf("alpha got calls %v next to a failing server", calls)
		}
	})
}
