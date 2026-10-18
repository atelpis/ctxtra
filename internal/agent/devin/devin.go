// Package devin supports Devin CLI and Devin Local, the agent of Devin
// Desktop (formerly Windsurf): a session-start hook in
// ~/.config/devin/config.json (or $XDG_CONFIG_HOME/devin), which both read.
// The file has Claude Code's hook layout, next to Devin's other settings.
//
// Devin also runs the hooks in ~/.claude/settings.json by default, so a
// session would get the files twice when both are set up. Claude's hook
// adds nothing then, see UnderDevin.
package devin

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/atelpis/ctxtra/internal/agent"
	"github.com/atelpis/ctxtra/internal/agent/hookjson"
	"github.com/atelpis/ctxtra/internal/paths"
	"github.com/atelpis/ctxtra/internal/resolve"
)

// Agent is Devin CLI and Devin Local.
type Agent struct{}

var _ agent.Agent = Agent{}

// events: Devin has no subagent-start event, and its matchers match tool
// names, so SessionStart has none.
var events = []hookjson.Event{{Name: "SessionStart"}}

func (Agent) Name() string { return "devin" }

// dir is Devin's user config folder, found the way Devin finds it.
func dir() string {
	if d := os.Getenv("XDG_CONFIG_HOME"); filepath.IsAbs(d) {
		return filepath.Join(d, "devin")
	}
	return filepath.Join(paths.Home(), ".config", "devin")
}

func (Agent) ConfigPath() string { return filepath.Join(dir(), "config.json") }

func (Agent) Detect() bool {
	if st, err := os.Stat(dir()); err == nil && st.IsDir() {
		return true
	}
	_, err := exec.LookPath("devin")
	return err == nil
}

func (a Agent) hooks() hookjson.Config {
	return hookjson.Config{Agent: a.Name(), Path: a.ConfigPath(), Events: events}
}

// Setup refuses a config with comments, which Devin allows: ctxtra writes
// plain JSON and would drop them.
func (a Agent) Setup(o agent.Options, r *agent.Report) error {
	b, err := os.ReadFile(a.ConfigPath())
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err == nil && len(bytes.TrimSpace(b)) > 0 && !json.Valid(b) {
		return fmt.Errorf("%s is not plain JSON (comments or trailing commas?); left alone: remove them, or add the hook by hand", a.ConfigPath())
	}
	return a.hooks().Apply(o, r)
}

func (a Agent) Status() agent.Status { return a.hooks().Status() }

func (Agent) Answer(event string, res *resolve.Result) any {
	return agent.AdditionalContext(event, res)
}

// Delivery: not measured; Devin may cut large hook output.
func (Agent) Delivery() agent.Delivery {
	return agent.Delivery{Title: "Devin", Capacity: 1_000_000}
}

// UnderDevin reports whether this hook process was started by Devin and
// ctxtra's Devin hook is set up, so that Devin's hook adds the files for
// this session. Devin sets DEVIN_PROJECT_DIR for the hooks it runs; Claude
// Code doesn't.
func UnderDevin() bool {
	if os.Getenv("DEVIN_PROJECT_DIR") == "" {
		return false
	}
	cmds, err := Agent{}.hooks().Commands()
	return err == nil && len(cmds["SessionStart"]) > 0
}
