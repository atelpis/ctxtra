// Package agent defines what ctxtra needs from a supported coding agent:
// how to set up its hooks, how to check them, and the format of the hook
// answer. Each agent is implemented in its own package under internal/agent;
// internal/agent/registry lists them.
package agent

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/atelpis/ctxtra/internal/chunk"
	"github.com/atelpis/ctxtra/internal/resolve"
)

// Agent is one supported coding agent.
type Agent interface {
	// Name is the agent's name on the command line and in hook commands.
	Name() string
	// Detect reports whether the agent looks installed on this machine.
	Detect() bool
	// ConfigPath is the user-level file that Setup changes.
	ConfigPath() string
	// Setup adds ctxtra's hooks, or removes them with o.Remove, and adds
	// what it changed to r.
	Setup(o Options, r *Report) error
	// Status describes the current setup, for doctor.
	Status() Status
	// Answer is the hook output for event. res is nil when there is nothing
	// to add, for example after an error.
	Answer(event string, res *resolve.Result) any
	// Delivery is how much text the agent gets in full, and in which parts.
	Delivery() Delivery
}

// Delivery describes how an agent gets the text. Some agents cut or drop a
// hook output above a size: ctxtra then splits the text into parts that fit,
// one per hook output, or per callback, or per injected step.
type Delivery struct {
	Title    string     // the agent's name in messages, for example "Claude Code"
	Capacity int        // the most text, in Unit, that a session gets in full
	Unit     chunk.Unit // of Capacity and Chunk
	// Chunk is the largest part of the text in one hook output; 0 when the
	// agent gets the whole text in one.
	Chunk int
	// ChunkJSON: Chunk counts the part as it is in the hook answer, in
	// chunk.JSON, not in Unit (Muse).
	ChunkJSON bool
	// Slots is how many parts the agent takes at one session start; 0 for any
	// number.
	Slots int
}

// Over names the limit, for messages: "Claude Code's limit of 360,000
// characters", "Copilot's limit of 140,000 bytes".
func (d Delivery) Over() string {
	return fmt.Sprintf("%s's limit of %s %s", d.Title, thousands(d.Capacity), d.Unit)
}

// Fits reports whether the agent gets all of res.
func (d Delivery) Fits(res *resolve.Result) bool {
	if d.Unit.Len(res.Render()) > d.Capacity {
		return false
	}
	return d.Slots == 0 || len(d.Chunks(res)) <= d.Slots
}

// Fit drops whole files from the end of res until the agent gets all of it.
func (d Delivery) Fit(res *resolve.Result) { res.Fit(d.Fits, d.Over()) }

// Chunks is the text of res in parts of at most d.Chunk.
func (d Delivery) Chunks(res *resolve.Result) []string {
	u := d.Unit
	if d.ChunkJSON {
		u = chunk.JSON
	}
	return chunk.Split(res.Blocks(), d.Chunk, u)
}

func thousands(n int) string {
	s := fmt.Sprint(n)
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	return s
}

// WithSlots is d for a setup with n slots, fewer than d.Slots.
func (d Delivery) WithSlots(n int) Delivery {
	if d.Slots == 0 || n <= 0 || n >= d.Slots {
		return d
	}
	d.Slots = n
	if d.Chunk > 0 && !d.ChunkJSON {
		d.Capacity = min(d.Capacity, n*d.Chunk)
	}
	return d
}

// Slotted is an agent whose config has Delivery().Slots hooks per event,
// which all run at once: hook K, run with --slot K --of N, adds part K of
// the text.
type Slotted interface {
	// SlotAnswer is the hook output with text, and with summary for the user
	// when it isn't "".
	SlotAnswer(event, text, summary string) any
}

// OldSlots is an agent with slots whose setups from before version 1 don't
// say how many slots they have (--of, ctxtra_slots). OldSlots is the count
// that all of them have, which stays when Delivery().Slots changes.
type OldSlots interface {
	OldSlots() int
}

// Skipper is an agent whose hook can also run in another agent's session,
// which that agent's own hook serves. Skip is why the hook adds nothing, or
// "" when it runs as usual.
type Skipper interface {
	Skip(in Input) string
}

// Upgrader is an agent whose hook, as an older version of ctxtra set it up,
// gets less text than Delivery. Legacy is what such a hook gets, and ok is
// false for a hook of this version.
type Upgrader interface {
	Legacy(event string, in Input) (d Delivery, ok bool)
}

// Missing is an agent whose hook answer can show the user a message.
type Missing interface {
	// MissingAnswer is the JSON that the hook command prints for event when
	// the binary is gone, with msg for the user; "" for {}.
	MissingAnswer(event, msg string) string
}

// Input is the part of a hook's input and command line that tells which
// agent and which setup runs it.
type Input struct {
	CursorVersion string // set by Cursor, also when it runs another agent's hooks
	Slot          int    // the hook's --slot, or -1 without one
	// Slots is how many slots the setup has: the hook's --of, or the
	// plugin's ctxtra_slots (Hermes); 0 when it doesn't say (OldSlots).
	Slots int
	// Plugin is the version of the plugin that runs ctxtra (Pi, OpenCode,
	// DSH, Hermes), 0 when it doesn't say.
	Plugin int
}

// AdditionalContext is the hook answer format that Claude Code, Codex CLI and
// Devin share: the text as additionalContext for the event. At session start,
// systemMessage also shows the user a one-line Summary; subagents don't get
// one, it would repeat for every subagent.
func AdditionalContext(event string, res *resolve.Result) any {
	return ContextAnswer(event, res.Render(), Summary(res))
}

// ContextAnswer is AdditionalContext with text and summary.
func ContextAnswer(event, text, summary string) any {
	out := map[string]any{}
	if text != "" {
		out["hookSpecificOutput"] = map[string]any{"hookEventName": event, "additionalContext": text}
	}
	if summary != "" && event == "SessionStart" {
		out["systemMessage"] = summary
	}
	return out
}

// SystemMessage is a MissingAnswer in the format of AdditionalContext: msg
// as systemMessage at session start, nothing for other events.
func SystemMessage(event, msg string) string {
	if event != "SessionStart" {
		return ""
	}
	var b strings.Builder
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if enc.Encode(map[string]string{"systemMessage": msg}) != nil {
		return ""
	}
	return strings.TrimSuffix(b.String(), "\n")
}

// summaryNames limits inline name lists in diagnostics.
const summaryNames = 3

// Summary lists the instructions and skills the session got, followed by notices. It is ""
// when ctxtra added nothing and has nothing to say.
func Summary(res *resolve.Result) string {
	if res == nil {
		return ""
	}
	var added, issues, lists []string
	if n := len(res.Files); n > 0 {
		names := make([]string, n)
		for i, h := range res.Files {
			names[i] = filepath.Base(h.Path)
		}
		added = append(added, plural(n, "instruction"))
		lists = append(lists, summaryList("Instructions", names))
	}
	if n := len(res.Skills); n > 0 {
		names := make([]string, n)
		for i, h := range res.Skills {
			names[i] = h.Name
		}
		added = append(added, plural(n, "skill"))
		lists = append(lists, summaryList("Skills", names))
	}
	if n := len(res.Missing); n > 0 {
		issues = append(issues, fmt.Sprintf("%d not found", n))
	}
	issues = append(issues, dropped(res.Dropped)...)
	if n := len(res.Problems); n > 0 {
		issues = append(issues, plural(n, "problem"))
	}
	if len(added) == 0 && len(issues) == 0 && len(res.Notices) == 0 {
		return ""
	}
	s := "ctxtra added nothing"
	if len(added) > 0 {
		s = "✓ ctxtra added " + strings.Join(added, " and ")
	}
	if len(issues) > 0 {
		s += "; " + strings.Join(issues, ", ") + " (run ctxtra doctor)"
	}
	for _, list := range lists {
		s += "\n" + list
	}
	for _, n := range res.Notices {
		s += "\nctxtra: " + n
	}
	return s
}

// dropped is a line per limit that files were dropped for: "dropped a.md,
// b.md (over budget.max-chars)".
func dropped(hits []resolve.Hit) []string {
	var out, over []string
	names := map[string][]string{}
	for _, h := range hits {
		if _, ok := names[h.Over]; !ok {
			over = append(over, h.Over)
		}
		names[h.Over] = append(names[h.Over], filepath.Base(h.Path))
	}
	for _, o := range over {
		out = append(out, fmt.Sprintf("dropped %s (over %s)", Names(names[o]), o))
	}
	return out
}

// summaryList shows at most 15 names, one per line.
func summaryList(title string, names []string) string {
	const limit = 15
	listed := make([]string, len(names))
	for i, name := range names {
		if strings.ContainsAny(name, "\r\n") || strings.HasPrefix(name, "+") && strings.HasSuffix(name, " more") {
			name = strconv.Quote(name)
		}
		listed[i] = name
	}
	if len(names) > limit {
		listed = append(listed[:limit:limit], fmt.Sprintf("+%d more", len(names)-limit))
	}
	return title + ":\n- " + strings.Join(listed, "\n- ")
}

// Names is "a, b, c", or "a, b, c, +2 more" for more than three names.
func Names(names []string) string {
	if len(names) > summaryNames {
		names = append(names[:summaryNames:summaryNames], fmt.Sprintf("+%d more", len(names)-summaryNames))
	}
	return strings.Join(names, ", ")
}

func plural(n int, what string) string {
	if n == 1 {
		return "1 " + what
	}
	return fmt.Sprintf("%d %ss", n, what)
}

// Options select what Setup does.
type Options struct {
	Binary string // absolute path of the binary the hooks run; not used with Remove
	Remove bool
	DryRun bool
}

// Report collects what Setup changed, or would change with DryRun.
type Report struct {
	Changes  []string // changed files
	Warnings []string
	Notes    []string // steps the user has to do, for example trusting hooks
}

// Status is the setup state of one agent.
type Status struct {
	State   string   // for example "set up for [SessionStart], runs ~/go/bin/ctxtra (ok)"
	OK      bool     // false when the hooks can't work as set up
	Details []string // extra lines, for example Codex trust records
}
