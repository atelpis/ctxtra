// Package muse supports Muse Code (Meta): hooks in the user settings file,
// $XDG_CONFIG_HOME/muse/settings.json or ~/.config/muse/settings.json, for
// session start and subagent start. User hooks run without project trust.
package muse

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/atelpis/ctxtra/internal/agent"
	"github.com/atelpis/ctxtra/internal/agent/hookjson"
	"github.com/atelpis/ctxtra/internal/chunk"
	"github.com/atelpis/ctxtra/internal/paths"
	"github.com/atelpis/ctxtra/internal/resolve"
)

// Agent is Muse Code.
type Agent struct{}

var (
	_ agent.Agent    = Agent{}
	_ agent.Slotted  = Agent{}
	_ agent.Upgrader = Agent{}
)

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
		Version: 1, VersionKey: "schema_version", Slots: a.Delivery().Slots}
}

func (a Agent) Setup(o agent.Options, r *agent.Report) error { return a.hooks().Apply(o, r) }

func (a Agent) Status() agent.Status { return a.hooks().Status() }

// Answer is the whole text in one hook, for a hook without a slot, set up
// by an older version.
func (Agent) Answer(event string, res *resolve.Result) any {
	return agent.ContextAnswer(event, res.Render(), clip(agent.Summary(res)))
}

func (Agent) SlotAnswer(event, text, summary string) any {
	return agent.ContextAnswer(event, text, clip(summary))
}

// maxSummary is the most of the summary in a hook answer, in bytes of
// JSON. Muse counts the whole answer, not only the text: with 13,000 bytes
// of text, one with a summary of 3,000 bytes passed, one with 8,000 was
// dropped (measured with Muse 1.4.10).
const maxSummary = 2000

// clip omits list entries before truncating diagnostics to fit maxSummary.
func clip(summary string) string {
	for chunk.JSON.Len(summary) > maxSummary {
		lines := strings.Split(summary, "\n")
		longest := -1
		inList := false
		for i, line := range lines {
			if line == "Instructions:" || line == "Skills:" {
				inList = true
				continue
			}
			if !strings.HasPrefix(line, "- ") {
				inList = false
			}
			if !inList {
				continue
			}
			if _, ok := omitted(line); ok {
				continue
			}
			if longest < 0 || chunk.JSON.Len(line) > chunk.JSON.Len(lines[longest]) {
				longest = i
			}
		}
		if longest < 0 {
			break
		}
		end := longest + 1
		for end < len(lines) && strings.HasPrefix(lines[end], "- ") {
			end++
		}
		count := 1
		if end > longest+1 {
			if n, ok := omitted(lines[end-1]); ok {
				count += n
				lines = append(lines[:end-1], lines[end:]...)
				end--
			}
		}
		lines = append(lines[:longest], lines[longest+1:]...)
		end--
		lines = append(lines[:end], append([]string{fmt.Sprintf("- +%d more", count)}, lines[end:]...)...)
		summary = strings.Join(lines, "\n")
	}
	if chunk.JSON.Len(summary) <= maxSummary {
		return summary
	}
	n := 0
	for i, r := range summary {
		if n += chunk.JSON.Len(string(r)); n > maxSummary-len("…") {
			return summary[:i] + "…"
		}
	}
	return summary
}

func omitted(line string) (int, bool) {
	if !strings.HasPrefix(line, "- +") || !strings.HasSuffix(line, " more") {
		return 0, false
	}
	n, err := strconv.Atoi(strings.TrimSuffix(strings.TrimPrefix(line, "- +"), " more"))
	return n, err == nil && n > 0
}

// Delivery: Muse drops all of a hook's text, without a word, above 15,040
// to 16,256 bytes, but gets the text of every hook of an event (measured
// with Muse 1.4.10): 40 hooks of up to 13,000 bytes each, counted as the
// text is in the hook's JSON answer, with its escapes. That leaves room for
// the rest of the answer and the summary. Text that isn't ASCII, and text
// with many quotes or backslashes, needs more parts, and fewer files fit
// then.
func (Agent) Delivery() agent.Delivery {
	return agent.Delivery{Title: "Muse", Capacity: 360_000, Unit: chunk.UTF16, Chunk: 13_000, ChunkJSON: true, Slots: 40}
}

// Legacy: a hook without a slot, set up by an older version, is the only
// one of its event, and gets one part.
func (a Agent) Legacy(_ string, in agent.Input) (agent.Delivery, bool) {
	d := a.Delivery()
	return agent.Delivery{Title: d.Title, Capacity: d.Chunk, Unit: chunk.JSON}, in.Slot < 0
}
