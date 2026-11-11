package e2e

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/ctxtra/ctxtra/e2e/driver"
)

// The agent learns from ctxtra's MCP server why a tool is missing: its
// instructions name the servers that need sign-in and point to ctxtra mcp
// status. Claude Code puts them in the system prompt, Codex in the
// description of the server's tools; other agents may drop them.
func TestMCPInstructions(t *testing.T) {
	forEachAgent(t, nil, func(t *testing.T, a Agent) {
		if a.Name() != "claude" && a.Name() != "codex" {
			t.Skip("checked in Claude Code and Codex")
		}
		s := newSession(t, a)
		s.Extra = append(s.Extra, "CTXTRA_EXPERIMENTAL_MCP=1")
		s.Repo("dev/svc", "git@github.com:Acme/Svc.git", nil)
		// Servers are declared in the config itself, not in a binding file.
		// A local server with tools too: Codex shows the instructions only
		// with the tools of the server that sent them.
		s.Write(".config/ctxtra/config.yaml", `schema: ctxtra/v1
mcp-servers:
  linear_work: {url: 'https://mcp.example.invalid/mcp'}
  probe: {command: [`+probeBinary(t)+`, mcp, -log, `+s.Path("probe.jsonl")+`]}
bindings:
  - name: svc
    match: {dirs: [~/dev/svc]}
    mcp-servers: [linear_work, probe]
`)
		s.Write(".ctxtra/mcp/state/linear_work.json",
			`{"version":1,"name":"linear_work","needs_login":true,"reason":"the sign-in expired or was revoked"}`)
		s.Setup()
		r := s.Run(s.Path("dev/svc"), driver.Options{})
		body, _ := json.Marshal(r.Requests[0].Body)
		for _, want := range []string{"run `ctxtra mcp status` in the session folder",
			"linear_work needs sign-in and has no tools now: the user runs `ctxtra mcp login linear_work`"} {
			if !strings.Contains(string(body), jsonText(want)) {
				t.Errorf("the model request lacks %q", want)
			}
		}
	})
}

// jsonText is s as it appears inside a JSON string.
func jsonText(s string) string {
	b, _ := json.Marshal(s)
	return string(b[1 : len(b)-1])
}

var probeOnce struct {
	sync.Once
	path string
	err  error
}

// probeBinary builds the recording MCP test server (mcpprobe/probe) once.
func probeBinary(t *testing.T) string {
	t.Helper()
	probeOnce.Do(func() {
		dir, err := os.MkdirTemp("", "ctxtra-e2e-probe-")
		if err != nil {
			probeOnce.err = err
			return
		}
		probeOnce.path = filepath.Join(dir, "probe")
		out, err := exec.Command("go", "build", "-o", probeOnce.path, "./mcpprobe/probe").CombinedOutput()
		if err != nil {
			probeOnce.err = fmt.Errorf("%v\n%s", err, out)
		}
	})
	if probeOnce.err != nil {
		t.Fatal(probeOnce.err)
	}
	return probeOnce.path
}
