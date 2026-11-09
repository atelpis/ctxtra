package cli

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/ctxtra/ctxtra/internal/agent"
	"github.com/ctxtra/ctxtra/internal/agent/registry"
	"github.com/ctxtra/ctxtra/internal/testenv"
)

// TestMCPClientContract: the MCP entry of every fixture (testdata/clients)
// starts this version's proxy, which serves the bound server's tools, also
// with an argument it doesn't know (docs/spec.md, Setup compatibility).
func TestMCPClientContract(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ran := 0
	for _, c := range testenv.Clients(t) {
		if !c.MCP {
			continue
		}
		ran++
		t.Run(c.ID+"/"+c.Agent, func(t *testing.T) {
			a, _ := registry.Get(c.Agent)
			m, ok := a.(agent.MCPAgent)
			if !ok {
				t.Fatalf("%s gets no MCP entry", c.Agent)
			}
			h := testenv.Home(t)
			c.Load(t, h, exe)
			argv, ok := m.MCP().MCPCommand()
			if !ok {
				t.Fatalf("no MCP entry of ctxtra's in %s", m.MCP().MCPPath())
			}
			cfg := testenv.Config(t, h, "schema: ctxtra/v1\nmcp-servers:\n  pg: {command: ['"+exe+"'], env: {CTXTRA_TEST_MCPSERVER: '1'}}\n"+
				"bindings:\n  - {name: all, match: {global: true}, mcp-servers: [pg]}\n")
			cmd := exec.Command(argv[0], append(argv[1:], "--from-a-later-version")...)
			cmd.Dir = h
			cmd.Env = []string{"HOME=" + h, "PATH=" + os.Getenv("PATH"), "CTXTRA_TEST_HOOK=1", "CTXTRA_EXPERIMENTAL_MCP=1",
				"CTXTRA_CONFIG=" + cfg, "CTXTRA_LOG=" + filepath.Join(h, "log.jsonl")}
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			cs, err := mcp.NewClient(&mcp.Implementation{Name: c.Agent, Version: "1"}, nil).Connect(ctx, &mcp.CommandTransport{Command: cmd}, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer cs.Close()
			res, err := cs.ListTools(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			var names []string
			for _, tool := range res.Tools {
				names = append(names, tool.Name)
			}
			if !slices.Contains(names, "pg__echo") {
				t.Fatalf("tools %v", names)
			}
			out, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "pg__echo", Arguments: map[string]any{"a": 1}})
			if err != nil || len(out.Content) != 1 || out.Content[0].(*mcp.TextContent).Text != `echo {"a":1}` {
				t.Errorf("call: %v %+v", err, out)
			}
		})
	}
	if ran == 0 {
		t.Fatal("no MCP fixtures")
	}
}
