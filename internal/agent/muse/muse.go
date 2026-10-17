// Package muse supports Muse Code (Meta): hooks in the user settings file,
// $XDG_CONFIG_HOME/muse/settings.json or ~/.config/muse/settings.json, for
// session start and subagent start. User hooks run without project trust.
package muse

import (
	"os"
	"os/exec"
	"path/filepath"

	"github.com/atelpis/ctxtra/internal/agent"
	"github.com/atelpis/ctxtra/internal/agent/hookjson"
	"github.com/atelpis/ctxtra/internal/paths"
	"github.com/atelpis/ctxtra/internal/resolve"
)

// Agent is Muse Code.
type Agent struct{}

var _ agent.Agent = Agent{}

// events: SessionStart also runs on resume, without a matcher. Muse replaces
// the text a hook added before, so a resumed session gets the current files
// once.
var events = []hookjson.Event{{Name: "SessionStart"}, {Name: "SubagentStart"}}

func (Agent) Name() string { return "muse" }

// dir is Muse's config folder, found the way Muse finds it.
func dir() string {
	if d := os.Getenv("XDG_CONFIG_HOME"); filepath.IsAbs(d) {
		return filepath.Join(d, "muse")
	}
	return filepath.Join(paths.Home(), ".config", "muse")
}

func (Agent) ConfigPath() string { return filepath.Join(dir(), "settings.json") }

// Detect also checks ~/.local/bin, where the install script puts the
// launcher without adding it to every PATH.
func (Agent) Detect() bool {
	if st, err := os.Stat(dir()); err == nil && st.IsDir() {
		return true
	}
	if _, err := os.Stat(filepath.Join(paths.Home(), ".local", "bin", "muse")); err == nil {
		return true
	}
	_, err := exec.LookPath("muse")
	return err == nil
}

// hooks: Muse refuses to start with a settings file without
// "schema_version": 1.
func (a Agent) hooks() hookjson.Config {
	return hookjson.Config{Agent: a.Name(), Path: a.ConfigPath(), Events: events,
		Version: 1, VersionKey: "schema_version"}
}

func (a Agent) Setup(o agent.Options, r *agent.Report) error { return a.hooks().Apply(o, r) }

func (a Agent) Status() agent.Status { return a.hooks().Status() }

func (Agent) Answer(event string, res *resolve.Result) any {
	return agent.AdditionalContext(event, res)
}

// PreviewLimit: Muse gives the model the whole hook text.
func (Agent) PreviewLimit() int { return 0 }
