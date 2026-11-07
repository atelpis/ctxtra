// Package muse supports Muse Code (Meta): hooks in the user settings file,
// $XDG_CONFIG_HOME/muse/settings.json or ~/.config/muse/settings.json, for
// session start and subagent start. User hooks run without project trust.
package muse

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
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
	_ agent.Missing  = Agent{}
	_ agent.Native   = Agent{}
	_ agent.Launcher = Agent{}
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
		Version: 1, VersionKey: "schema_version", Slots: a.Delivery().Slots, Missing: a}
}

func (a Agent) Setup(o agent.Options, r *agent.Report) error { return a.hooks().Apply(o, r) }

func (a Agent) Status(bin string, known bool) agent.Status { return a.hooks().Status(bin, known) }

func (a Agent) Check(bin string, known bool) []string { return a.hooks().Check(bin, known) }

// Answer is the whole text in one hook. The hooks that setup writes have
// slots, and get SlotAnswer.
func (Agent) Answer(event string, res *resolve.Result) any {
	return agent.ContextAnswer(event, res.Render(), clip(agent.Summary(res)))
}

func (Agent) SlotAnswer(event, text, summary string) any {
	return agent.ContextAnswer(event, text, clip(summary))
}

// MissingAnswer: Muse shows systemMessage like Claude Code.
func (Agent) MissingAnswer(event, msg string) string { return agent.SystemMessage(event, msg) }

// maxSummary is the most of the summary in a hook answer, in bytes of
// JSON. Muse counts the whole answer, not only the text: with 13,000 bytes
// of text, one with a summary of 3,000 bytes passed, one with 8,000 was
// dropped (measured with Muse 1.4.10).
const maxSummary = 2000

// clip makes the summary fit maxSummary. It omits list entries first, the
// longest first, counted in its group's "+N more"; then whole groups, the
// longest name first, counted in one line at the end of their list. Only
// then it cuts the end. A line after the lists tells where to see them all.
func clip(summary string) string {
	for chunk.JSON.Len(summary) > maxSummary {
		lines := strings.Split(summary, "\n")
		next, ok := omitEntry(lines)
		if !ok {
			next, ok = omitGroup(lines)
		}
		if !ok {
			break
		}
		if !slices.Contains(next, agent.FullList) {
			next = slices.Insert(next, listsEnd(next), agent.FullList)
		}
		summary = strings.Join(next, "\n")
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

type kind int

const (
	other  kind = iota
	title       // "Instructions:" or "Skills:"
	header      // a group's name, or the line of omitted groups
	entry       // an entry or a group's "+N more"
)

// kinds tells what each line of a summary is.
func kinds(lines []string) []kind {
	out := make([]kind, len(lines))
	inList := false
	for i, line := range lines {
		switch {
		case line == "Instructions:" || line == "Skills:":
			out[i], inList = title, true
		case !inList:
		case strings.HasPrefix(line, agent.EntryPrefix):
			out[i] = entry
		case strings.HasPrefix(line, agent.GroupIndent):
			out[i] = header
		default:
			inList = false
		}
	}
	return out
}

// listsEnd is the index after the last line of the lists.
func listsEnd(lines []string) int {
	end := 0
	for i, k := range kinds(lines) {
		if k != other {
			end = i + 1
		}
	}
	return end
}

// longest is the index of the longest line of kind k that keep accepts, or -1.
func longest(lines []string, k kind, keep func(string) bool) int {
	best := -1
	for i, ki := range kinds(lines) {
		if ki == k && keep(lines[i]) && (best < 0 || chunk.JSON.Len(lines[i]) > chunk.JSON.Len(lines[best])) {
			best = i
		}
	}
	return best
}

// omitEntry omits the longest entry and counts it in its group's "+N more".
func omitEntry(lines []string) ([]string, bool) {
	i := longest(lines, entry, func(l string) bool { _, ok := omitted(l); return !ok })
	if i < 0 {
		return nil, false
	}
	end := i + 1
	for end < len(lines) && strings.HasPrefix(lines[end], agent.EntryPrefix) {
		end++
	}
	tail, count := end, 1
	if n, ok := omitted(lines[end-1]); ok {
		count += n
		end--
	}
	more := fmt.Sprintf("%s+%d more", agent.EntryPrefix, count)
	return slices.Concat(lines[:i], lines[i+1:end], []string{more}, lines[tail:]), true
}

// omitGroup omits the group with the longest name and counts its entries in
// the line of omitted groups at the end of its list.
func omitGroup(lines []string) ([]string, bool) {
	i := longest(lines, header, func(l string) bool { _, _, ok := omittedGroups(l); return !ok })
	if i < 0 {
		return nil, false
	}
	end, count := i+1, counted(lines[i])
	for end < len(lines) && strings.HasPrefix(lines[end], agent.EntryPrefix) {
		n, ok := omitted(lines[end])
		if !ok {
			n = 1
		}
		count += n
		end++
	}
	lines = slices.Concat(lines[:i], lines[end:])
	k := kinds(lines)
	end = i
	for end < len(lines) && (k[end] == header || k[end] == entry) {
		end++
	}
	groups := 1
	if n, g, ok := omittedGroups(lines[end-1]); ok && k[end-1] == header {
		count, groups = count+n, groups+g
		lines = slices.Delete(lines, end-1, end)
		end--
	}
	return slices.Insert(lines, end, fmt.Sprintf("%s+%d more from %s", agent.GroupIndent, count, plural(groups, "source"))), true
}

// counted is the number of entries on a group's line, "  name (N)", or 0.
func counted(header string) int {
	rest, ok := strings.CutSuffix(header, ")")
	if !ok {
		return 0
	}
	_, num, ok := strings.Cut(rest, " (")
	n, err := strconv.Atoi(num)
	if !ok || err != nil || n < 0 {
		return 0
	}
	return n
}

func omitted(line string) (int, bool) {
	rest, ok := strings.CutPrefix(line, agent.EntryPrefix+"+")
	if !ok || !strings.HasSuffix(rest, " more") {
		return 0, false
	}
	n, err := strconv.Atoi(strings.TrimSuffix(rest, " more"))
	return n, err == nil && n > 0
}

// omittedGroups reads the line of omitted groups: "  +N more from G sources".
func omittedGroups(line string) (n, groups int, ok bool) {
	_, err := fmt.Sscanf(line, agent.GroupIndent+"+%d more from %d source", &n, &groups)
	return n, groups, err == nil && n >= 0 && groups > 0 && line == fmt.Sprintf("%s+%d more from %s", agent.GroupIndent, n, plural(groups, "source"))
}

func plural(n int, what string) string {
	if n == 1 {
		return "1 " + what
	}
	return fmt.Sprintf("%d %ss", n, what)
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

// NativeFiles: Muse reads AGENTS.md in its config folder.
func (Agent) NativeFiles() []agent.NativeFile {
	if f, ok := agent.ReadNative(filepath.Join(dir(), "AGENTS.md")); ok {
		return []agent.NativeFile{f}
	}
	return nil
}

func (Agent) Launch(prompt string) []string { return []string{"muse", prompt} }
