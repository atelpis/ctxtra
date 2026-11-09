package muse

import (
	"time"

	"github.com/ctxtra/ctxtra/internal/agent"
	"github.com/ctxtra/ctxtra/internal/agent/mcpentry"
)

var _ agent.MCPAgent = Agent{}

// MCP: in the same settings.json as the hooks. An optional server doesn't
// stop a session that it can't start.
func (a Agent) MCP() agent.MCPHost {
	return mcpentry.Config{Agent: a.Name(), Path: a.ConfigPath(), Servers: []string{"mcpServers"},
		Extra: []mcpentry.KV{{Key: "type", Value: "stdio"}, {Key: "mode", Value: "optional"}}}
}

func (Agent) MCPDeadline() time.Duration { return agent.MCPDeadline }
