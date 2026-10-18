// Package cursor supports Cursor Agent CLI: a sessionStart hook in
// ~/.cursor/hooks.json.
package cursor

import (
	"os"
	"os/exec"
	"path/filepath"

	"github.com/atelpis/ctxtra/internal/agent"
	"github.com/atelpis/ctxtra/internal/agent/hookjson"
	"github.com/atelpis/ctxtra/internal/paths"
	"github.com/atelpis/ctxtra/internal/resolve"
)

// Agent is Cursor Agent CLI.
type Agent struct{}

var _ agent.Agent = Agent{}

func (Agent) Name() string { return "cursor" }

func dir() string { return filepath.Join(paths.Home(), ".cursor") }

func (Agent) ConfigPath() string { return filepath.Join(dir(), "hooks.json") }

func (Agent) Detect() bool {
	if st, err := os.Stat(dir()); err == nil && st.IsDir() {
		return true
	}
	_, err := exec.LookPath("cursor-agent")
	return err == nil
}

func (a Agent) hooks() hookjson.Config {
	return hookjson.Config{Agent: a.Name(), Path: a.ConfigPath(), Events: []hookjson.Event{{Name: "sessionStart"}},
		Flat: true, Version: 1}
}

func (a Agent) Setup(o agent.Options, r *agent.Report) error { return a.hooks().Apply(o, r) }

func (a Agent) Status() agent.Status { return a.hooks().Status() }

func (Agent) Answer(_ string, res *resolve.Result) any {
	text := res.Render()
	if text == "" {
		return map[string]any{}
	}
	return map[string]any{"additional_context": text}
}

// Delivery: not measured; Cursor may cut large hook output.
func (Agent) Delivery() agent.Delivery {
	return agent.Delivery{Title: "Cursor", Capacity: 1_000_000}
}
