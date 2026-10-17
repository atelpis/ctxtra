// Package hookjson edits the JSON hook configs of Claude Code, Codex CLI,
// Cursor and Copilot CLI. It changes only ctxtra's own entries: other tools' entries keep
// their content and order, and our entries are updated in place, because
// Codex keys hook trust by position.
package hookjson

import (
	"fmt"
	"os"
	"slices"

	"github.com/atelpis/ctxtra/internal/agent"
	"github.com/atelpis/ctxtra/internal/fsutil"
	"github.com/atelpis/ctxtra/internal/jsonorder"
	"github.com/atelpis/ctxtra/internal/paths"
)

// Timeout is the hook timeout in seconds written to every entry.
const Timeout = 10

// Event is one hook event and its matcher ("" for none).
type Event struct{ Name, Matcher string }

// Config describes one agent's hook file.
type Config struct {
	Agent  string
	Path   string
	Events []Event
	// Flat is the layout of Cursor and Copilot: each entry is a handler. Otherwise (Claude,
	// Codex) each entry is a group with a matcher and a list of handlers.
	Flat bool
	// StatusMessage names the hook in Codex's /hooks review.
	StatusMessage string
	// Version is written as the top-level "version" if missing (Cursor,
	// Copilot).
	Version int
	// Own is a file that only ctxtra writes (Copilot): --remove deletes it
	// when nothing but the version is left.
	Own bool
}

// Apply adds, updates or removes our entries and writes the file when it
// changes.
func (c Config) Apply(o agent.Options, r *agent.Report) error {
	data, err := c.read()
	if err != nil {
		return err
	}
	hooks := jsonorder.NewMap()
	if v, ok := data.Get("hooks"); ok {
		h, ok := v.(*jsonorder.Map)
		if !ok {
			return fmt.Errorf("%s: \"hooks\" is not an object; left alone", c.Path)
		}
		hooks = h
	}
	add := !o.Remove
	if !add && !c.hasOurs(hooks) {
		return nil // nothing to remove: leave the file as it is
	}
	for _, ev := range c.Events {
		var list []any
		if v, ok := hooks.Get(ev.Name); ok {
			arr, ok := v.([]any)
			if !ok {
				return fmt.Errorf("%s: hooks.%s is not a list; left alone", c.Path, ev.Name)
			}
			list = arr
		}
		want, handler := c.entry(o.Binary, ev)
		if add && !c.Flat && c.countOurs(list) > 1 {
			r.Warnings = append(r.Warnings, fmt.Sprintf("%s: removed duplicate ctxtra %s hooks; hooks after them moved", c.Agent, ev.Name))
		}
		// Ours: entries that only run our command, or single handlers in a
		// group edited by hand. Setup updates the first one in place and
		// removes the rest; --remove removes all.
		placed := !add
		kept := []any{}
		for _, e := range list {
			if c.isOurs(e) {
				if !placed {
					kept = append(kept, want)
					placed = true
				}
				continue
			}
			if !c.Flat {
				var repl any
				if !placed {
					repl = handler
				}
				if updateMixed(e, repl) && repl != nil {
					placed = true
				}
			}
			kept = append(kept, e)
		}
		if !placed {
			kept = append(kept, want)
		}
		if len(kept) > 0 {
			hooks.Set(ev.Name, kept)
		} else {
			hooks.Delete(ev.Name)
		}
	}
	if len(hooks.Keys) > 0 {
		if _, ok := data.Get("version"); c.Version != 0 && add && !ok {
			data.Set("version", c.Version)
		}
		data.Set("hooks", hooks)
	} else {
		data.Delete("hooks")
	}
	// A symlink (for example into a dotfiles repo) is not deleted: its
	// target is written below, like any other change.
	_, version := data.Get("version")
	if st, err := os.Lstat(c.Path); err == nil && st.Mode().IsRegular() && c.Own && !add && len(data.Keys) == 1 && version {
		r.Changes = append(r.Changes, c.Path)
		if o.DryRun {
			return nil
		}
		old, err := os.ReadFile(c.Path)
		if err != nil {
			return err
		}
		if err := fsutil.Backup(c.Path, old, paths.BackupDir()); err != nil {
			return err
		}
		return os.Remove(c.Path)
	}
	out, err := jsonorder.MarshalIndent(data)
	if err != nil {
		return err
	}
	path, changed, err := fsutil.WriteAtomic(c.Path, out, paths.BackupDir(), o.DryRun)
	if changed {
		r.Changes = append(r.Changes, path)
	}
	return err
}

// Commands returns our hook commands by event, in file order.
func (c Config) Commands() (map[string][]Command, error) {
	data, err := c.read()
	if err != nil {
		return nil, err
	}
	out := map[string][]Command{}
	for _, ev := range c.Events {
		for gi, e := range entries(data, ev.Name) {
			if c.Flat {
				if cmd, ok := ourCommand(commandOf(e)); ok {
					out[ev.Name] = append(out[ev.Name], Command{cmd, gi, 0})
				}
				continue
			}
			for hi, h := range handlers(e) {
				if cmd, ok := ourCommand(commandOf(h)); ok {
					out[ev.Name] = append(out[ev.Name], Command{cmd, gi, hi})
				}
			}
		}
	}
	return out, nil
}

// Command is one of our hook commands and its position in the file.
type Command struct {
	Text           string
	Group, Handler int
}

// Status describes our entries and the binary they run.
func (c Config) Status() agent.Status {
	cmds, err := c.Commands()
	if err != nil {
		return agent.Status{State: "unreadable: " + err.Error()}
	}
	var evs, bins []string
	for _, ev := range c.Events {
		if len(cmds[ev.Name]) == 0 {
			continue
		}
		evs = append(evs, ev.Name)
		for _, cmd := range cmds[ev.Name] {
			if bin, _ := agent.CommandBinary(cmd.Text); !slices.Contains(bins, bin) {
				bins = append(bins, bin)
			}
		}
	}
	if len(evs) == 0 {
		return agent.Status{State: "not set up", OK: true}
	}
	return BinaryStatus(fmt.Sprintf("set up for %v", evs), bins)
}

// BinaryStatus adds the binaries the hooks run, and whether they exist, to
// state.
func BinaryStatus(state string, bins []string) agent.Status {
	s := agent.Status{State: state, OK: true}
	for _, bin := range bins {
		st, ok := agent.CheckBinary(bin)
		s.OK = s.OK && ok
		s.State += fmt.Sprintf(", runs %s (%s)", paths.Tilde(bin), st)
	}
	if !s.OK {
		s.State += "; run ctxtra setup"
	}
	return s
}

// entry is the entry setup writes for one event, and for grouped layouts
// its single handler.
func (c Config) entry(bin string, ev Event) (entry, handler *jsonorder.Map) {
	entry = jsonorder.NewMap()
	cmd := agent.Command(bin, c.Agent, ev.Name)
	if c.Flat {
		entry.Set("command", cmd)
		entry.Set("timeout", Timeout)
		return entry, nil
	}
	handler = jsonorder.NewMap()
	handler.Set("type", "command")
	handler.Set("command", cmd)
	handler.Set("timeout", Timeout)
	if c.StatusMessage != "" {
		handler.Set("statusMessage", c.StatusMessage)
	}
	if ev.Matcher != "" {
		entry.Set("matcher", ev.Matcher)
	}
	entry.Set("hooks", []any{handler})
	return entry, handler
}

func (c Config) read() (*jsonorder.Map, error) {
	data, err := jsonorder.ReadFile(c.Path)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", c.Path, err)
	}
	return data, nil
}

func (c Config) hasOurs(hooks *jsonorder.Map) bool {
	for _, ev := range c.Events {
		v, _ := hooks.Get(ev.Name)
		if list, ok := v.([]any); ok && c.countOurs(list) > 0 {
			return true
		}
	}
	return false
}

func (c Config) countOurs(list []any) int {
	n := 0
	for _, e := range list {
		if c.isOurs(e) {
			n++
			continue
		}
		for _, h := range handlers(e) {
			if _, ok := ourCommand(commandOf(h)); ok {
				n++
			}
		}
	}
	return n
}

// isOurs reports whether an entry belongs to ctxtra alone: a flat entry
// with our command, or a group whose handlers all run our command.
func (c Config) isOurs(e any) bool {
	if c.Flat {
		_, ok := ourCommand(commandOf(e))
		return ok
	}
	hs := handlers(e)
	if len(hs) == 0 {
		return false
	}
	for _, h := range hs {
		if _, ok := ourCommand(commandOf(h)); !ok {
			return false
		}
	}
	return true
}

func ourCommand(v any) (string, bool) {
	cmd, ok := v.(string)
	if !ok {
		return "", false
	}
	_, ok = agent.CommandBinary(cmd)
	return cmd, ok
}

// updateMixed handles a group with our handler next to other tools'
// handlers (edited by hand). With replacement set, the first of our handlers
// is replaced in place, so trust positions stay; other ones are removed. It
// reports whether the group had one of our handlers.
func updateMixed(e any, replacement any) bool {
	o, ok := e.(*jsonorder.Map)
	hs := handlers(e)
	if !ok || hs == nil {
		return false
	}
	found := false
	kept := []any{}
	for _, h := range hs {
		_, ours := ourCommand(commandOf(h))
		switch {
		case !ours:
			kept = append(kept, h)
		case !found && replacement != nil:
			kept = append(kept, replacement)
			found = true
		default:
			found = true
		}
	}
	if found {
		o.Set("hooks", kept)
	}
	return found
}

// handlers returns a hook group's handler list, or nil.
func handlers(e any) []any {
	if o, ok := e.(*jsonorder.Map); ok {
		if v, ok := o.Get("hooks"); ok {
			if hs, ok := v.([]any); ok {
				return hs
			}
		}
	}
	return nil
}

func commandOf(h any) any {
	if o, ok := h.(*jsonorder.Map); ok {
		v, _ := o.Get("command")
		return v
	}
	return nil
}

// entries returns the hook list for one event, or nil.
func entries(data *jsonorder.Map, event string) []any {
	hv, _ := data.Get("hooks")
	hooks, ok := hv.(*jsonorder.Map)
	if !ok {
		return nil
	}
	v, _ := hooks.Get(event)
	list, _ := v.([]any)
	return list
}
