// Package hookjson edits the JSON hook configs of Claude Code, Codex CLI,
// Muse Code, Cursor, Copilot CLI, Antigravity and Devin. It changes only ctxtra's own entries:
// other tools' entries keep their content and order, and our entries are
// updated in place, because Codex keys hook trust by position.
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
	// Flat is the layout of Cursor, Copilot and Antigravity: each entry is a handler.
	// Otherwise (Claude, Codex, Muse, Devin) each entry is a group with a matcher
	// and a list of handlers.
	Flat bool
	// StatusMessage names the hook in Codex's /hooks review.
	StatusMessage string
	// Version is written as the top-level VersionKey if missing: "version"
	// (the default) for Cursor and Copilot, "schema_version" for Muse.
	Version    int
	VersionKey string
	// Own is a file that only ctxtra writes (Copilot): --remove deletes it
	// when nothing but the version is left.
	Own bool
	// Key overrides the top-level event list key (Antigravity uses "ctxtra").
	Key string
	// Slots is the number of our entries per event for a grouped layout, each
	// running agent.SlotCommand (Claude, Muse); 0 for one entry with
	// agent.Command.
	Slots int
	// ContextLimit is written as additionalContextLimit to each handler
	// (Codex); 0 for none.
	ContextLimit int
}

func (c Config) key() string {
	if c.Key == "" {
		return "hooks"
	}
	return c.Key
}

// Apply adds, updates or removes our entries and writes the file when it
// changes.
func (c Config) Apply(o agent.Options, r *agent.Report) error {
	data, err := c.read()
	if err != nil {
		return err
	}
	hooks := jsonorder.NewMap()
	if v, ok := data.Get(c.key()); ok {
		h, ok := v.(*jsonorder.Map)
		if !ok {
			return fmt.Errorf("%s: %q is not an object; left alone", c.Path, c.key())
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
				return fmt.Errorf("%s: %s.%s is not a list; left alone", c.Path, c.key(), ev.Name)
			}
			list = arr
		}
		want, handler := c.wanted(o.Binary, ev)
		if add && !c.Flat && c.countOurs(list) > len(want) {
			r.Warnings = append(r.Warnings, fmt.Sprintf("%s: removed duplicate ctxtra %s hooks; hooks after them moved", c.Agent, ev.Name))
		}
		// Ours: entries that only run our command, or single handlers in a
		// group edited by hand. Setup updates the first one in place and
		// removes the rest; --remove removes all. With slots, our entries
		// take the place of the first one, and a group edited by hand loses
		// our handlers and gets our entries after it.
		placed := !add
		kept := []any{}
		for _, e := range list {
			if c.isOurs(e) {
				if !placed {
					kept = append(kept, want...)
					placed = true
				}
				continue
			}
			if !c.Flat {
				var repl any
				if !placed {
					repl = handler
				}
				if updateMixed(e, repl) && !placed {
					placed = true
					if repl == nil {
						kept = append(kept, e)
						kept = append(kept, want...)
						continue
					}
				}
			}
			kept = append(kept, e)
		}
		if !placed {
			kept = append(kept, want...)
		}
		if len(kept) > 0 {
			hooks.Set(ev.Name, kept)
		} else {
			hooks.Delete(ev.Name)
		}
	}
	if len(hooks.Keys) > 0 {
		if _, ok := data.Get(c.versionKey()); c.Version != 0 && add && !ok {
			data.Set(c.versionKey(), c.Version)
		}
		data.Set(c.key(), hooks)
	} else {
		data.Delete(c.key())
	}
	// A symlink (for example into a dotfiles repo) is not deleted: its
	// target is written below, like any other change.
	_, version := data.Get(c.versionKey())
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

func (c Config) versionKey() string {
	if c.VersionKey == "" {
		return "version"
	}
	return c.VersionKey
}

// Commands returns our hook commands by event, in file order.
func (c Config) Commands() (map[string][]Command, error) {
	data, err := c.read()
	if err != nil {
		return nil, err
	}
	out := map[string][]Command{}
	for _, ev := range c.Events {
		for gi, e := range c.entries(data, ev.Name) {
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
	s := BinaryStatus(fmt.Sprintf("set up for %v", evs), bins)
	if c.ContextLimit > 0 {
		data, _ := c.read()
		for _, ev := range c.Events {
			for _, cmd := range cmds[ev.Name] {
				if limit(data, c.entries(data, ev.Name), cmd) != fmt.Sprint(c.ContextLimit) {
					s.OK = false
					s.Details = append(s.Details, fmt.Sprintf("%s: a ctxtra hook without additionalContextLimit %d, which cuts the text (run ctxtra setup)", ev.Name, c.ContextLimit))
				}
			}
		}
	}
	if c.Slots > 0 {
		for _, ev := range c.Events {
			if n := slots(cmds[ev.Name]); n != c.Slots {
				s.OK = false
				s.Details = append(s.Details, fmt.Sprintf("%s: %d of the %d ctxtra hooks (run ctxtra setup)", ev.Name, n, c.Slots))
			}
		}
	}
	return s
}

// Limited reports whether event has one of our handlers, and each of them
// has additionalContextLimit ContextLimit. It is false when the file can't
// be read.
func (c Config) Limited(event string) bool {
	data, err := c.read()
	if err != nil {
		return false
	}
	list, found := c.entries(data, event), false
	for gi, e := range list {
		for hi, h := range handlers(e) {
			if cmd, ok := ourCommand(commandOf(h)); ok {
				if limit(data, list, Command{cmd, gi, hi}) != fmt.Sprint(c.ContextLimit) {
					return false
				}
				found = true
			}
		}
	}
	return found
}

// limit is the additionalContextLimit of the handler of cmd, as written.
func limit(data *jsonorder.Map, list []any, cmd Command) string {
	if data == nil || cmd.Group >= len(list) {
		return ""
	}
	hs := handlers(list[cmd.Group])
	if cmd.Handler >= len(hs) {
		return ""
	}
	h, ok := hs[cmd.Handler].(*jsonorder.Map)
	if !ok {
		return ""
	}
	v, ok := h.Get("additionalContextLimit")
	if !ok {
		return ""
	}
	return fmt.Sprint(v)
}

// slots is how many different slots cmds run, or 0 when one of them has no
// slot or two have the same.
func slots(cmds []Command) int {
	seen := map[int]bool{}
	for _, cmd := range cmds {
		n, ok := agent.CommandSlot(cmd.Text)
		if !ok || seen[n] {
			return 0
		}
		seen[n] = true
	}
	return len(seen)
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

// wanted are the entries setup writes for one event, and for a grouped
// layout without slots the single handler, to update a group edited by hand
// in place.
func (c Config) wanted(bin string, ev Event) (entries []any, handler any) {
	if c.Slots == 0 {
		e, h := c.entry(agent.Command(bin, c.Agent, ev.Name), ev)
		if h == nil {
			return []any{e}, nil
		}
		return []any{e}, h
	}
	for n := range c.Slots {
		e, _ := c.entry(agent.SlotCommand(bin, c.Agent, ev.Name, n), ev)
		entries = append(entries, e)
	}
	return entries, nil
}

// entry is one entry that runs cmd, and for a grouped layout its handler.
func (c Config) entry(cmd string, ev Event) (entry, handler *jsonorder.Map) {
	entry = jsonorder.NewMap()
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
	if c.ContextLimit > 0 {
		handler.Set("additionalContextLimit", c.ContextLimit)
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
func (c Config) entries(data *jsonorder.Map, event string) []any {
	hv, _ := data.Get(c.key())
	hooks, ok := hv.(*jsonorder.Map)
	if !ok {
		return nil
	}
	v, _ := hooks.Get(event)
	list, _ := v.([]any)
	return list
}
