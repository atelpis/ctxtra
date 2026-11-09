package cursor

import (
	"path/filepath"
	"time"

	"github.com/ctxtra/ctxtra/internal/agent"
	"github.com/ctxtra/ctxtra/internal/agent/mcpentry"
)

var _ agent.MCPAgent = Agent{}

// MCP: from Cursor's docs; not checked with Cursor itself yet.
func (a Agent) MCP() agent.MCPHost {
	return mcpentry.Config{Agent: a.Name(), Path: filepath.Join(dir(), "mcp.json"), Servers: []string{"mcpServers"}}
}

// MCPDeadline: not measured in Cursor.
func (Agent) MCPDeadline() time.Duration { return agent.MCPDeadline }
