package pi

import (
	"path/filepath"
	"time"

	"github.com/ctxtra/ctxtra/internal/agent"
	"github.com/ctxtra/ctxtra/internal/agent/mcpentry"
)

var _ agent.MCPAgent = Agent{}

func (a Agent) MCP() agent.MCPHost {
	return mcpentry.Config{Agent: a.Name(), Path: filepath.Join(dir(), "mcp.json"), Servers: []string{"mcpServers"}}
}

// MCPDeadline: Pi waits about 10 seconds for the first tool list (measured
// with Pi 1.5.0).
func (Agent) MCPDeadline() time.Duration { return 8 * time.Second }
