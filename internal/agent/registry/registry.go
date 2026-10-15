// Package registry lists the supported agents.
package registry

import (
	"github.com/atelpis/ctxtra/internal/agent"
	"github.com/atelpis/ctxtra/internal/agent/claude"
	"github.com/atelpis/ctxtra/internal/agent/codex"
	"github.com/atelpis/ctxtra/internal/agent/cursor"
	"github.com/atelpis/ctxtra/internal/agent/pi"
)

var all = []agent.Agent{claude.Agent{}, codex.Agent{}, pi.Agent{}, cursor.Agent{}}

// All returns every supported agent.
func All() []agent.Agent { return all }

// Names returns the names of every supported agent.
func Names() []string {
	names := make([]string, len(all))
	for i, a := range all {
		names[i] = a.Name()
	}
	return names
}

// Get returns the agent with this name.
func Get(name string) (agent.Agent, bool) {
	for _, a := range all {
		if a.Name() == name {
			return a, true
		}
	}
	return nil, false
}
