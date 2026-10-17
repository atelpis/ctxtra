// Package antigravity supports Google Antigravity: a SessionStart hook named
// "ctxtra" in ~/.gemini/config/hooks.json, which the Antigravity CLI (agy),
// App and IDE share. The hook also runs for every subagent, as its own
// session.
package antigravity

import (
	"os"
	"os/exec"
	"path/filepath"

	"github.com/atelpis/ctxtra/internal/agent"
	"github.com/atelpis/ctxtra/internal/agent/hookjson"
	"github.com/atelpis/ctxtra/internal/jsonorder"
	"github.com/atelpis/ctxtra/internal/paths"
	"github.com/atelpis/ctxtra/internal/resolve"
)

// Agent is Google Antigravity.
type Agent struct{}

var _ agent.Agent = Agent{}

// hookName is the key of our hook in hooks.json.
const hookName = "ctxtra"

func (Agent) Name() string { return "antigravity" }

func dir() string { return filepath.Join(paths.Home(), ".gemini") }

func (Agent) ConfigPath() string { return filepath.Join(dir(), "config", "hooks.json") }

// Detect looks for the app data folders of the CLI, App and IDE, not
// ~/.gemini alone, which Gemini CLI also uses.
func (Agent) Detect() bool {
	for _, d := range []string{"antigravity-cli", "antigravity", "antigravity-ide"} {
		if st, err := os.Stat(filepath.Join(dir(), d)); err == nil && st.IsDir() {
			return true
		}
	}
	_, err := exec.LookPath("agy")
	return err == nil
}

func (a Agent) hooks() hookjson.Config {
	return hookjson.Config{Agent: a.Name(), Path: a.ConfigPath(), Events: []hookjson.Event{{Name: "SessionStart"}},
		Flat: true, Key: hookName}
}

func (a Agent) Setup(o agent.Options, r *agent.Report) error { return a.hooks().Apply(o, r) }

// Status also reports a hook turned off with "enabled": false, for example
// in the App's settings.
func (a Agent) Status() agent.Status {
	s := a.hooks().Status()
	data, err := jsonorder.ReadFile(a.ConfigPath())
	if err != nil {
		return s
	}
	if v, _ := data.Get(hookName); v != nil {
		if h, ok := v.(*jsonorder.Map); ok {
			if on, ok := h.Get("enabled"); ok && on == false {
				s.OK = false
				s.State += `; turned off ("enabled": false)`
			}
		}
	}
	return s
}

// Answer injects the text as a system message. Antigravity has no message
// for the user.
func (Agent) Answer(_ string, res *resolve.Result) any {
	text := res.Render()
	if text == "" {
		return map[string]any{}
	}
	return map[string]any{"injectSteps": []any{map[string]any{"systemMessage": map[string]any{"systemMessage": text}}}}
}

func (Agent) PreviewLimit() int { return 0 }

var _ agent.ByteLimiter = Agent{}

// ByteLimit: the CLI cuts hook text after about 48,900 bytes (measured with
// agy 1.4.0).
func (Agent) ByteLimit() int { return 48000 }
