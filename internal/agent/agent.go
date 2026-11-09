// Package agent defines what ctxtra needs from a supported coding agent:
// how to set up its hooks, how to check them, and the format of the hook
// answer. Each agent is implemented in its own package under internal/agent;
// internal/agent/registry lists them.
package agent

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/ctxtra/ctxtra/internal/chunk"
	"github.com/ctxtra/ctxtra/internal/config"
	"github.com/ctxtra/ctxtra/internal/fsutil"
	"github.com/ctxtra/ctxtra/internal/paths"
	"github.com/ctxtra/ctxtra/internal/resolve"
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
	// Status describes the current setup, for doctor, with Check's notices
	// as details.
	Status(bin string, known bool) Status
	// Check returns what the user should do about this agent's setup, a line
	// each without the agent's name, from its files only: it runs nothing.
	// known is whether setup.json lists the agent's current config path as
	// set up, so a setup that is gone is told from one that never was. bin
	// is the running binary, "" when it can't be compared.
	Check(bin string, known bool) []string
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

// PluginClient is an agent whose hook runs from ctxtra's plugin (Pi,
// OpenCode, DSH, Hermes), which sends its protocol version as
// ctxtra_plugin. Protocol is the version that setup writes.
type PluginClient interface {
	Protocol() int
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

// Retrust is an agent that runs a changed hook only after the user trusts
// it again (Codex): a notice whose command changes its hooks says so.
type Retrust interface {
	// TrustNote is what the user is asked then, as a clause: "Codex will
	// then ask you to trust the hooks".
	TrustNote() string
}

// Missing is an agent whose hook answer can show the user a message.
type Missing interface {
	// MissingAnswer is the JSON that the hook command prints for event when
	// the binary is gone, with msg for the user; "" for {}.
	MissingAnswer(event, msg string) string
}

// Native is an agent with user-level instruction files of its own, which
// setup offers to move into ctxtra.
type Native interface {
	// NativeFiles are those of the files that have text, or can't be read,
	// in the order the agent reads them.
	NativeFiles() []NativeFile
}

// NativeFile is a user-level instruction file of an agent.
type NativeFile struct {
	Path string
	Note string // for example why the agent doesn't read it now; "" for none
	Err  error  // why it can't be read
}

// ReadNative is the file at path, and whether it counts: it has more than
// white space, or it can't be read as a regular file of at most
// config.MaxFileBytes, which Err says.
func ReadNative(path string) (NativeFile, bool) {
	b, err := fsutil.ReadBounded(path, config.MaxFileBytes)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return NativeFile{}, false
	case err != nil:
		return NativeFile{Path: path, Err: err}, true
	}
	return NativeFile{Path: path}, strings.TrimSpace(string(b)) != ""
}

// WalkNative is ReadNative of the files in root and its subfolders whose
// names end with suffix, by name. It follows symlinks, and reads each folder
// once. A missing root, or a broken symlink, adds nothing; a folder that
// can't be read is a file with Err.
func WalkNative(root, suffix string) []NativeFile {
	var out []NativeFile
	seen := map[string]bool{}
	var walk func(string)
	walk = func(d string) {
		real, err := filepath.EvalSymlinks(d)
		if err != nil {
			if !errors.Is(err, fs.ErrNotExist) {
				out = append(out, NativeFile{Path: d, Err: err})
			}
			return
		}
		if seen[real] {
			return
		}
		seen[real] = true
		entries, err := os.ReadDir(d)
		if err != nil {
			out = append(out, NativeFile{Path: d, Err: err})
		}
		for _, e := range entries {
			p := filepath.Join(d, e.Name())
			st, err := os.Stat(p)
			switch {
			case errors.Is(err, fs.ErrNotExist):
			case err != nil:
				out = append(out, NativeFile{Path: p, Err: err})
			case st.IsDir():
				walk(p)
			case strings.HasSuffix(p, suffix):
				if f, ok := ReadNative(p); ok {
					out = append(out, f)
				}
			}
		}
	}
	walk(root)
	return out
}

// Launcher is an agent with a CLI that starts an interactive session with a
// first prompt.
type Launcher interface {
	// Launch is the command line, whose first element is looked up in PATH.
	Launch(prompt string) []string
}

// Input is the part of a hook's input and command line that tells which
// agent and which setup runs it.
type Input struct {
	CursorVersion string // set by Cursor, also when it runs another agent's hooks
	Slot          int    // the hook's --slot, or -1 without one
	// Slots is how many slots the setup has: the hook's --of, or the
	// plugin's ctxtra_slots (Hermes); 0 when it doesn't say.
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

// Summary lists the instructions and skills the session got, by source, as
// res.Summary chooses, followed by notices. It is "" when ctxtra added nothing and has nothing to
// say.
func Summary(res *resolve.Result) string {
	if res == nil {
		return ""
	}
	var added, issues, lists []string
	cut := false
	if n := len(res.Files); n > 0 {
		added = append(added, plural(n, "instruction"))
		if res.Summary.ShowInstructions {
			list, c := summaryList("Instructions", res.Files, fileEntry, "", res.Summary.MaxInstructions)
			lists, cut = append(lists, list), cut || c
		}
	}
	if n := len(res.Skills); n > 0 {
		added = append(added, plural(n, "skill"))
		if res.Summary.ShowSkills {
			skills := paths.Real(filepath.Join(paths.StateDir(), "skills"))
			list, c := summaryList("Skills", res.Skills, func(h resolve.Hit) string { return h.Name }, skills, res.Summary.MaxSkills)
			lists, cut = append(lists, list), cut || c
		}
	}
	if cut {
		lists = append(lists, FullList)
	}
	if n := len(res.Missing); n > 0 {
		issues = append(issues, fmt.Sprintf("%d not found", n))
	}
	// Before the dropped files, whose paths can be long: Muse cuts the end of
	// a summary that is too long for it.
	if n := len(res.Problems); n > 0 {
		issues = append(issues, plural(n, "problem"))
	}
	issues = append(issues, dropped(res.Dropped, res.Namer())...)
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
	// After an empty line, each with its own marker: agents indent the lines
	// after the first, so a notice right after a list reads as part of it.
	if len(res.Notices) > 0 {
		s += "\n"
		for _, n := range res.Notices {
			s += "\nℹ " + strings.ReplaceAll(n, "\n", "\n  ")
		}
	}
	return s
}

// Summary lines: a group per source under each list title, its entries
// after it, or its number of entries in parentheses when the list shows
// none. FullList follows the lists when entries were left out.
const (
	GroupIndent = "  "
	EntryPrefix = "  - "
	FullList    = "Full list: ctxtra context files"
)

// fileEntry is a file by its path in its source; outside sources, by its
// full path, which no other file has.
func fileEntry(h resolve.Hit) string {
	if h.Source != "" {
		return h.SourcePath
	}
	return paths.TildeReal(h.Path)
}

// group is the title of h's group: its source, "ctxtra" for a skill in
// ctxtra's skills folder (skills, "" for files), or "local".
func group(h resolve.Hit, skills string) (key, title string) {
	if h.Source != "" {
		return "source:" + h.Source, h.Source
	}
	if _, ok := paths.Rel(skills, h.Path); ok && skills != "" {
		return "ctxtra", "ctxtra"
	}
	return "local", "local"
}

// summaryList shows hits by group, in the order of each group's first hit,
// at most max of a group followed by "+N more". With max 0, a group is its
// title and its number of hits. It reports whether it left any out.
func summaryList(title string, hits []resolve.Hit, entry func(resolve.Hit) string, skills string, max int) (string, bool) {
	var keys []string
	titles, entries := map[string]string{}, map[string][]string{}
	for _, h := range hits {
		k, t := group(h, skills)
		if _, ok := titles[k]; !ok {
			keys, titles[k] = append(keys, k), t
		}
		entries[k] = append(entries[k], entry(h))
	}
	var b strings.Builder
	b.WriteString(title + ":")
	cut := false
	for _, k := range keys {
		b.WriteString("\n" + GroupIndent + titles[k])
		list := entries[k]
		if max == 0 {
			fmt.Fprintf(&b, " (%d)", len(list))
			cut = true
			continue
		}
		more := 0
		if len(list) > max {
			list, more, cut = list[:max], len(list)-max, true
		}
		for _, e := range list {
			b.WriteString("\n" + EntryPrefix + plain(e))
		}
		if more > 0 {
			fmt.Fprintf(&b, "\n%s+%d more", EntryPrefix, more)
		}
	}
	return b.String(), cut
}

// dropped is a line per limit that files were dropped for: "dropped a.md,
// b.md (over budget.max-chars)".
func dropped(hits []resolve.Hit, name func(resolve.Hit) string) []string {
	var out, over []string
	names := map[string][]string{}
	for _, h := range hits {
		if _, ok := names[h.Over]; !ok {
			over = append(over, h.Over)
		}
		names[h.Over] = append(names[h.Over], plain(name(h)))
	}
	for _, o := range over {
		out = append(out, fmt.Sprintf("dropped %s (over %s)", Names(names[o]), o))
	}
	return out
}

// plain is name, quoted when it would read as more than one name: a file name
// can hold a line end, or look like "+N more".
func plain(name string) string {
	if strings.ContainsAny(name, "\r\n") || strings.HasPrefix(name, "+") && strings.HasSuffix(name, " more") {
		return strconv.Quote(name)
	}
	return name
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

// MCPAgent is an agent that gets ctxtra's MCP entry: one stdio MCP server
// named ctxtra that runs ctxtra mcp proxy AGENT.
type MCPAgent interface {
	MCP() MCPHost
}

// MCPHost edits and checks an agent's MCP entry.
type MCPHost interface {
	// MCPPath is the file the entry is in.
	MCPPath() string
	// SetupMCP adds, updates or removes the entry, as o says.
	SetupMCP(o MCPOptions, r *Report) error
	// MCPStatus describes the entry, for doctor, with CheckMCP's notices.
	MCPStatus(bin string, o MCPCheck) Status
	// CheckMCP returns what the user should do about the entry, from its
	// file only.
	CheckMCP(bin string, o MCPCheck) []string
	// MCPCommand is the program and arguments that the entry runs; false
	// without an entry of ctxtra's.
	MCPCommand() ([]string, bool)
}

// MCPOptions select what SetupMCP does.
type MCPOptions struct {
	Options
	// Want is whether the agent should have the entry: the config declares
	// MCP servers. Without it, setup adds no entry and keeps one that is
	// there; Remove removes it either way.
	Want bool
	// EnvPass are the env-pass names of every local server, which Codex
	// passes to MCP servers only when its entry lists them.
	EnvPass []string
}

// MCPCheck is what CheckMCP needs besides the file.
type MCPCheck struct {
	Known bool // setup.json lists the entry as set up
	Want  bool // the config declares MCP servers
}

// MCPEntryName is the name of ctxtra's entry in every agent.
const MCPEntryName = "ctxtra"

// MCPArgs are the arguments of the entry's command for agent.
func MCPArgs(agent string) []string { return []string{"mcp", "proxy", agent} }
