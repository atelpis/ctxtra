// Package claude supports Claude Code: hooks in ~/.claude/settings.json (or
// $CLAUDE_CONFIG_DIR) for session start and subagent start.
package claude

import (
	"os"
	"os/exec"
	"path/filepath"

	"github.com/atelpis/ctxtra/internal/agent"
	"github.com/atelpis/ctxtra/internal/agent/devin"
	"github.com/atelpis/ctxtra/internal/agent/hookjson"
	"github.com/atelpis/ctxtra/internal/chunk"
	"github.com/atelpis/ctxtra/internal/paths"
	"github.com/atelpis/ctxtra/internal/resolve"
)

// Agent is Claude Code.
type Agent struct{}

var (
	_ agent.Agent   = Agent{}
	_ agent.Slotted = Agent{}
)

// events are not registered for resume: the resumed transcript already
// contains the injected text.
var events = []hookjson.Event{{Name: "SessionStart", Matcher: "startup|clear|compact"}, {Name: "SubagentStart"}}

func (Agent) Name() string { return "claude" }

func dir() string {
	if d := os.Getenv("CLAUDE_CONFIG_DIR"); d != "" {
		return d
	}
	return filepath.Join(paths.Home(), ".claude")
}

func (Agent) ConfigPath() string { return filepath.Join(dir(), "settings.json") }

func (Agent) Detect() bool {
	if st, err := os.Stat(dir()); err == nil && st.IsDir() {
		return true
	}
	_, err := exec.LookPath("claude")
	return err == nil
}

func (a Agent) hooks() hookjson.Config {
	return hookjson.Config{Agent: a.Name(), Path: a.ConfigPath(), Events: events, Slots: a.Delivery().Slots}
}

func (a Agent) Setup(o agent.Options, r *agent.Report) error { return a.hooks().Apply(o, r) }

func (a Agent) Status() agent.Status { return a.hooks().Status() }

// Skip: Devin also runs the hooks in ~/.claude/settings.json, and when
// ctxtra's Devin hook is set up, the session would get the files twice.
func (Agent) Skip() string {
	if devin.UnderDevin() {
		return "run by Devin, whose own ctxtra hook adds the files"
	}
	return ""
}

// Answer is the whole text in one hook, for a hook without a slot, set up
// by an older version.
func (Agent) Answer(event string, res *resolve.Result) any {
	return agent.AdditionalContext(event, res)
}

func (Agent) SlotAnswer(event, text, summary string) any {
	return agent.ContextAnswer(event, text, summary)
}

// Delivery: Claude shows the model only a preview of a hook's text over
// 10,000 UTF-16 units, but gets the text of every hook of an event: 40 hooks
// of up to 9,000 each (measured with Claude Code 2.1.300).
func (Agent) Delivery() agent.Delivery {
	return agent.Delivery{Title: "Claude Code", Capacity: 360_000, Unit: chunk.UTF16, Chunk: 9000, Slots: 40}
}
