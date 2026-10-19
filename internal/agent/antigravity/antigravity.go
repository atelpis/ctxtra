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
	"github.com/atelpis/ctxtra/internal/chunk"
	"github.com/atelpis/ctxtra/internal/fsutil"
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
func (a Agent) Status(bin string, known bool) agent.Status {
	s := a.hooks().Status(bin, known)
	b, err := fsutil.ReadBounded(a.ConfigPath(), agent.MaxConfig)
	if err != nil {
		return s
	}
	v, _ := jsonorder.Decode(b)
	data, ok := v.(*jsonorder.Map)
	if !ok {
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

func (a Agent) Check(bin string, known bool) []string { return a.hooks().Check(bin, known) }

// Answer injects the text as system messages, a step per part.
// Antigravity has no message for the user.
func (a Agent) Answer(_ string, res *resolve.Result) any {
	var steps []any
	for _, text := range a.Delivery().Chunks(res) {
		steps = append(steps, map[string]any{"systemMessage": map[string]any{"systemMessage": text}})
	}
	if len(steps) == 0 {
		return map[string]any{}
	}
	return map[string]any{"injectSteps": steps}
}

// Delivery: the CLI cuts an injected step after about 48,900 bytes, and of
// all steps together keeps the last 686,000 bytes or so: the earlier steps
// are lost (measured with agy 1.4.0).
func (Agent) Delivery() agent.Delivery {
	return agent.Delivery{Title: "Antigravity", Capacity: 650_000, Unit: chunk.Bytes, Chunk: 40_000}
}
