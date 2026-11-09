package claude

import (
	"os"
	"path/filepath"

	"github.com/ctxtra/ctxtra/internal/agent"
	"github.com/ctxtra/ctxtra/internal/agent/mcpentry"
	"github.com/ctxtra/ctxtra/internal/paths"
)

var _ agent.MCPAgent = Agent{}

// MCP: user-level MCP servers are in ~/.claude.json, which is next to
// ~/.claude, not in it; with $CLAUDE_CONFIG_DIR, in that folder.
func (a Agent) MCP() agent.MCPHost {
	path := filepath.Join(paths.Home(), ".claude.json")
	if d := os.Getenv("CLAUDE_CONFIG_DIR"); d != "" {
		path = filepath.Join(d, ".claude.json")
	}
	return mcpentry.Config{Agent: a.Name(), Path: path, Servers: []string{"mcpServers"}, Extra: []mcpentry.KV{{Key: "type", Value: "stdio"}}}
}
