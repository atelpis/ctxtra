package cli

import (
	"testing"
	"time"

	"github.com/ctxtra/ctxtra/internal/agent"
	"github.com/ctxtra/ctxtra/internal/agent/registry"
)

// Each agent's proxy answers the first tool list before the agent stops
// waiting for it; an agent this version doesn't know gets the longest wait.
func TestMCPDeadline(t *testing.T) {
	for name, want := range map[string]time.Duration{"claude": 1500 * time.Millisecond, "pi": 8 * time.Second, "codex": 10 * time.Second,
		"muse": 10 * time.Second, "unknown-agent": agent.MCPDeadline} {
		if got := mcpDeadline(name); got != want {
			t.Errorf("%s: %v, want %v", name, got, want)
		}
	}
	for _, a := range registry.All() {
		if m, ok := a.(agent.MCPAgent); ok && (m.MCPDeadline() <= 0 || m.MCPDeadline() > agent.MCPDeadline) {
			t.Errorf("%s: deadline %v", a.Name(), m.MCPDeadline())
		}
	}
}
