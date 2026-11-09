package antigravity

import (
	"path/filepath"
	"time"

	"github.com/ctxtra/ctxtra/internal/agent"
	"github.com/ctxtra/ctxtra/internal/agent/mcpentry"
)

var _ agent.MCPAgent = Agent{}

func (a Agent) MCP() agent.MCPHost {
	return mcpentry.Config{Agent: a.Name(), Path: filepath.Join(dir(), "config", "mcp_config.json"), Servers: []string{"mcpServers"}}
}

func (Agent) MCPDeadline() time.Duration { return agent.MCPDeadline }
