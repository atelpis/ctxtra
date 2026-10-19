// Package hook is the entry point the agents run at session start. It never
// breaks a session: on any error it injects nothing and logs the error.
package hook

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"slices"
	"strconv"
	"time"

	"github.com/atelpis/ctxtra/internal/agent"
	"github.com/atelpis/ctxtra/internal/agent/registry"
	"github.com/atelpis/ctxtra/internal/gitrepo"
	"github.com/atelpis/ctxtra/internal/jsonorder"
	"github.com/atelpis/ctxtra/internal/paths"
	"github.com/atelpis/ctxtra/internal/resolve"
	"github.com/atelpis/ctxtra/internal/skill"
)

const maxInput = 10 << 20

// payload is the part of the agents' hook input that ctxtra uses.
type payload struct {
	Cwd            string          `json:"cwd"`
	WorkspaceRoots []string        `json:"workspace_roots"` // Cursor
	WorkspacePaths []string        `json:"workspacePaths"`  // Antigravity
	Source         string          `json:"source"`
	AgentType      string          `json:"agent_type"`
	SessionID      string          `json:"session_id"`
	AgentID        string          `json:"agent_id"` // a Claude subagent, whose session_id is its parent's
	CursorVersion  string          `json:"cursor_version"`
	Plugin         int             `json:"ctxtra_plugin"` // the version of ctxtra's plugin that runs the hook
	Slots          json.RawMessage `json:"ctxtra_slots"`  // the callbacks that ctxtra's Hermes plugin registered
}

// Args are the hook's options on its command line.
type Args struct {
	Slot  int   // --slot, or -1 without one
	Slots int   // --of, or 0 without one
	Err   error // the options can't be read: the hook adds nothing
}

// Run reads the hook input from in and writes the agent's answer to out. It
// always returns 0. An unknown agent gets "{}".
func Run(harness, event string, in io.Reader, out, errOut io.Writer) int {
	return RunSlot(harness, event, -1, in, out, errOut)
}

// RunSlot is Run for hook slot of an agent.Slotted, or for no slot when it
// is below 0. Other agents ignore the slot.
func RunSlot(harness, event string, slot int, in io.Reader, out, errOut io.Writer) int {
	return RunArgs(harness, event, Args{Slot: slot}, in, out, errOut)
}

// RunArgs is Run with the hook's options.
func RunArgs(harness, event string, args Args, in io.Reader, out, errOut io.Writer) (code int) {
	slot := args.Slot
	a, ok := registry.Get(harness)
	empty := "{}"
	if ok {
		if b, err := jsonorder.Marshal(a.Answer(event, nil)); err == nil {
			empty = string(b)
		}
	}
	ts := time.Now().Format(time.RFC3339)
	defer func() {
		if r := recover(); r != nil {
			logEntry(Entry{Ts: ts, Harness: harness, Event: event, Error: fmt.Sprint(r)})
			fmt.Fprintln(out, empty)
			code = 0
		}
	}()
	if !ok {
		logEntry(Entry{Ts: ts, Harness: harness, Event: event, Error: "unknown agent"})
		fmt.Fprintln(out, empty)
		return 0
	}
	if args.Err != nil {
		io.Copy(io.Discard, io.LimitReader(in, maxInput))
		logEntry(Entry{Ts: ts, Harness: harness, Event: event, Error: "bad hook arguments: " + args.Err.Error()})
		fmt.Fprintln(out, empty)
		return 0
	}
	var p payload
	raw, _ := io.ReadAll(io.LimitReader(in, maxInput))
	if len(bytes.TrimSpace(raw)) > 0 {
		if err := json.Unmarshal(raw, &p); err != nil {
			logEntry(Entry{Ts: ts, Harness: harness, Event: event, Error: "bad hook input: " + err.Error()})
			fmt.Fprintln(out, empty)
			return 0
		}
	}
	dir := p.Cwd
	if dir == "" && len(p.WorkspaceRoots) > 0 {
		dir = p.WorkspaceRoots[0]
	}
	// Antigravity runs hooks in the hook file's folder, not the session's.
	if dir == "" && len(p.WorkspacePaths) > 0 {
		dir = p.WorkspacePaths[0]
	}
	if dir == "" {
		dir, _ = os.Getwd()
	}
	slots := args.Slots
	if p.Slots != nil {
		n, err := strconv.Atoi(string(p.Slots))
		if err != nil || n <= 0 {
			logEntry(Entry{Ts: ts, Harness: harness, Event: event, Error: fmt.Sprintf("bad hook input: ctxtra_slots %s is not a positive integer", p.Slots)})
			fmt.Fprintln(out, empty)
			return 0
		}
		if slots == 0 {
			slots = n
		}
	}
	input := agent.Input{CursorVersion: p.CursorVersion, Slot: max(slot, -1), Slots: slots, Plugin: p.Plugin}
	// Before anything slow: an agent that doesn't wait for its hooks may run
	// this one, and all slots of it.
	if s, ok := a.(agent.Skipper); ok {
		if why := s.Skip(input); why != "" {
			if slot <= 0 {
				e := Entry{Ts: ts, Harness: harness, Event: event, Dir: dir, Source: p.Source, AgentType: p.AgentType, Skipped: why}
				if r := gitrepo.Identify(dir); r != nil { // for doctor, which finds runs by repository
					e.Repo = r.Root
				}
				logEntry(e)
			}
			fmt.Fprintln(out, empty)
			return 0
		}
	}
	d, upgrade := delivery(a, event, input)
	if sl, ok := a.(agent.Slotted); ok && slot >= 0 {
		// A slot of a setup with more slots than this version has, after a
		// downgrade.
		if d.Slots > 0 && slot >= d.Slots {
			fmt.Fprintln(out, empty)
			return 0
		}
		runSlot(a, sl, d, upgrade, slot, ts, event, p, input, dir, out, errOut)
		return 0
	}
	res, e := session(a, d, upgrade, ts, event, p, dir)
	logEntry(e)
	for _, m := range res.Missing {
		fmt.Fprintf(errOut, "ctxtra: bound file not found: %s\n", m)
	}
	write(out, a.Answer(event, res))
	return 0
}

// delivery is what the hook that in describes gets: what a hook that an
// older version set up got then, with a notice that says to run setup, or
// the agent's Delivery in the slots that its setup has. A setup that
// doesn't say has agent.OldSlots.
func delivery(a agent.Agent, event string, in agent.Input) (d agent.Delivery, upgrade string) {
	if u, ok := a.(agent.Upgrader); ok {
		if old, ok := u.Legacy(event, in); ok {
			return old, fmt.Sprintf("the hook is from an older version of ctxtra, with %s; run ctxtra setup", old.Over())
		}
	}
	n := in.Slots
	if o, ok := a.(agent.OldSlots); ok && n == 0 {
		n = o.OldSlots()
	}
	return a.Delivery().WithSlots(n), ""
}

// session resolves what a session in dir gets, with the files that d
// doesn't get in full dropped, and its log entry. The user and the log get
// the notice upgrade.
func session(a agent.Agent, d agent.Delivery, upgrade, ts, event string, p payload, dir string) (*resolve.Result, Entry) {
	// Before resolving: the session lists the skill as it is on disk.
	skillErr := skill.Refresh()
	res, _ := resolve.Session(dir, paths.ConfigPath())
	if upgrade != "" {
		res.Notices = append(slices.Clip(res.Notices), upgrade)
	}
	d.Fit(res)
	e := newEntry(ts, a.Name(), event, p, res, resolve.Chars(res.Render()))
	if upgrade != "" {
		e.Problems = append(slices.Clip(e.Problems), upgrade)
	}
	if skillErr != nil {
		e.Problems = append(slices.Clip(e.Problems), "can't update the ctxtra skill: "+skillErr.Error())
	}
	return res, e
}

func write(out io.Writer, answer any) {
	b, err := jsonorder.Marshal(answer)
	if err != nil {
		panic(err)
	}
	fmt.Fprintln(out, string(b))
}
