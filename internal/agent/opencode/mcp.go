package opencode

import (
	"path/filepath"
	"time"

	"github.com/ctxtra/ctxtra/internal/agent"
	"github.com/ctxtra/ctxtra/internal/agent/mcpentry"
)

var _ agent.MCPAgent = Agent{}

// MCP: not pooled, so that OpenCode's shared service starts one proxy per
// session folder, and each gets the servers of its own folder.
func (a Agent) MCP() agent.MCPHost {
	return mcpentry.Config{Agent: a.Name(), Path: filepath.Join(dir(), "opencode.json"), Servers: []string{"mcp", "servers"},
		CommandArray: true, Extra: []mcpentry.KV{{Key: "type", Value: "local"}, {Key: "pool", Value: false}}}
}

// MCPDeadline: OpenCode leaves MCP tools out of its first model request
// anyway, and adds them later.
func (Agent) MCPDeadline() time.Duration { return agent.MCPDeadline }
