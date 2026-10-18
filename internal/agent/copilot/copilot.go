// Package copilot supports GitHub Copilot CLI: hooks for session start and
// subagent start in ~/.copilot/hooks/ctxtra.json (or $COPILOT_HOME), a file
// of ctxtra's own next to the user's other hook files.
package copilot

import (
	"os"
	"os/exec"
	"path/filepath"

	"github.com/atelpis/ctxtra/internal/agent"
	"github.com/atelpis/ctxtra/internal/agent/hookjson"
	"github.com/atelpis/ctxtra/internal/chunk"
	"github.com/atelpis/ctxtra/internal/paths"
	"github.com/atelpis/ctxtra/internal/resolve"
)

// Agent is GitHub Copilot CLI.
type Agent struct{}

var _ agent.Agent = Agent{}

// events: sessionStart also runs for resumed sessions, and adds the text
// again. Copilot doesn't keep a hook's text in the session, so a resumed
// session doesn't have it otherwise.
var events = []hookjson.Event{{Name: "sessionStart"}, {Name: "subagentStart"}}

func (Agent) Name() string { return "copilot" }

func dir() string {
	if d := os.Getenv("COPILOT_HOME"); d != "" {
		return d
	}
	return filepath.Join(paths.Home(), ".copilot")
}

func (Agent) ConfigPath() string { return filepath.Join(dir(), "hooks", "ctxtra.json") }

func (Agent) Detect() bool {
	if st, err := os.Stat(dir()); err == nil && st.IsDir() {
		return true
	}
	_, err := exec.LookPath("copilot")
	return err == nil
}

func (a Agent) hooks() hookjson.Config {
	return hookjson.Config{Agent: a.Name(), Path: a.ConfigPath(), Events: events, Flat: true, Version: 1, Own: true}
}

func (a Agent) Setup(o agent.Options, r *agent.Report) error { return a.hooks().Apply(o, r) }

func (a Agent) Status() agent.Status { return a.hooks().Status() }

func (Agent) Answer(_ string, res *resolve.Result) any {
	text := res.Render()
	if text == "" {
		return map[string]any{}
	}
	return map[string]any{"additionalContext": text}
}

// Delivery: Copilot drops all hook text, without a word, when it doesn't fit
// the model's context window; several hooks count together. With Copilot
// 1.0.98 and an unknown model, about 150,000 tokens pass: 600,000 ASCII
// characters of plain prose, but 150,000 CJK characters, and not 194,000
// characters of short words and punctuation ("x!x!"). A tokenizer that
// works on bytes makes at most one token of a byte, so bytes of UTF-8 are an
// upper bound on the tokens of any text: a capacity of 140,000 bytes leaves
// room for Copilot's own prompt. It costs English text: about 140,000
// characters fit, where about 600,000 would.
func (Agent) Delivery() agent.Delivery {
	return agent.Delivery{Title: "Copilot", Capacity: 140_000, Unit: chunk.Bytes}
}
