// Package mcpentry edits ctxtra's MCP entry in the agents' JSON config
// files: one server named ctxtra that runs ctxtra mcp proxy AGENT. Setup
// owns only the keys it writes; every other key in the entry, and every
// other entry, stays as it is.
package mcpentry

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"slices"

	"github.com/ctxtra/ctxtra/internal/agent"
	"github.com/ctxtra/ctxtra/internal/fsutil"
	"github.com/ctxtra/ctxtra/internal/jsonorder"
	"github.com/ctxtra/ctxtra/internal/paths"
)

// Config describes one agent's MCP file.
type Config struct {
	Agent string
	Path  string
	// Servers is the path to the object of MCP servers, such as
	// ["mcpServers"], or ["mcp", "servers"] for OpenCode.
	Servers []string
	// CommandArray: the command and its arguments are one list under
	// "command" (OpenCode); otherwise "command" is the program and "args"
	// its arguments.
	CommandArray bool
	// Extra are keys setup also owns, with their values, in this order: for
	// example "type": "stdio".
	Extra []KV
}

var _ agent.MCPHost = Config{}

func (c Config) MCPPath() string { return c.Path }

// KV is a key and its value.
type KV struct {
	Key   string
	Value any
}

// handEdited explains why setup leaves an entry alone.
const handEdited = "the ctxtra MCP server doesn't run ctxtra mcp proxy"

// owned are the keys and values setup writes for bin.
func (c Config) owned(bin string) []KV {
	args := agent.MCPArgs(c.Agent)
	var out []KV
	for _, kv := range c.Extra {
		if kv.Key == "type" {
			out = append(out, kv)
		}
	}
	if c.CommandArray {
		out = append(out, KV{"command", toAny(append([]string{bin}, args...))})
	} else {
		out = append(out, KV{"command", bin}, KV{"args", toAny(args)})
	}
	for _, kv := range c.Extra {
		if kv.Key != "type" {
			out = append(out, kv)
		}
	}
	return out
}

func toAny(s []string) []any {
	out := make([]any, len(s))
	for i, v := range s {
		out[i] = v
	}
	return out
}

// SetupMCP adds, updates or removes the entry.
func (c Config) SetupMCP(o agent.MCPOptions, r *agent.Report) error {
	if !o.Remove && !o.Want {
		return nil
	}
	data, err := c.load()
	if err != nil {
		return fmt.Errorf("%s: %w", c.Path, err)
	}
	servers, err := c.servers(data, !o.Remove)
	if err != nil {
		return err
	}
	cur, has := entry(servers)
	switch {
	case has && cur == nil, has && !c.ours(cur):
		again := "ctxtra setup"
		if o.Remove {
			again += " --remove"
		}
		return fmt.Errorf("%s: %s; left alone. Rename or delete it, then run %s --agent %s", c.Path, handEdited, again, c.Agent)
	case o.Remove && !has:
		return nil
	case o.Remove:
		servers.Delete(agent.MCPEntryName)
		c.prune(data)
	default:
		if cur == nil {
			cur = jsonorder.NewMap()
		}
		for _, kv := range c.owned(o.Binary) {
			cur.Set(kv.Key, kv.Value)
		}
		servers.Set(agent.MCPEntryName, cur)
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

// load parses the file, an empty object when it is missing or blank.
func (c Config) load() (*jsonorder.Map, error) {
	b, err := fsutil.ReadBounded(c.Path, agent.MaxConfig)
	if errors.Is(err, os.ErrNotExist) || err == nil && len(bytes.TrimSpace(b)) == 0 {
		return jsonorder.NewMap(), nil
	}
	if err != nil {
		return nil, err
	}
	v, err := jsonorder.Decode(b)
	if err != nil {
		return nil, err
	}
	o, ok := v.(*jsonorder.Map)
	if !ok {
		return nil, errors.New("expected a JSON object")
	}
	return o, nil
}

// servers is the object of MCP servers in data, created when create is
// set; nil when it isn't there.
func (c Config) servers(data *jsonorder.Map, create bool) (*jsonorder.Map, error) {
	cur := data
	for i, k := range c.Servers {
		v, ok := cur.Get(k)
		if !ok {
			if !create {
				return jsonorder.NewMap(), nil
			}
			m := jsonorder.NewMap()
			cur.Set(k, m)
			cur = m
			continue
		}
		m, ok := v.(*jsonorder.Map)
		if !ok {
			return nil, fmt.Errorf("%s: %s is not an object; left alone", c.Path, dotted(c.Servers[:i+1]))
		}
		cur = m
	}
	return cur, nil
}

// prune removes the objects on the way to the servers that are now empty.
func (c Config) prune(data *jsonorder.Map) {
	for n := len(c.Servers); n > 0; n-- {
		parent := data
		for _, k := range c.Servers[:n-1] {
			v, _ := parent.Get(k)
			parent, _ = v.(*jsonorder.Map)
		}
		v, _ := parent.Get(c.Servers[n-1])
		if m, ok := v.(*jsonorder.Map); !ok || len(m.Keys) > 0 {
			return
		}
		parent.Delete(c.Servers[n-1])
	}
}

func dotted(keys []string) string {
	s := ""
	for i, k := range keys {
		if i > 0 {
			s += "."
		}
		s += k
	}
	return s
}

// entry is the ctxtra entry: has reports whether the name is there, and
// cur is nil when it isn't an object.
func entry(servers *jsonorder.Map) (cur *jsonorder.Map, has bool) {
	v, ok := servers.Get(agent.MCPEntryName)
	if !ok {
		return nil, false
	}
	m, _ := v.(*jsonorder.Map)
	return m, true
}

// commandLine is an entry's program and arguments.
func (c Config) commandLine(e *jsonorder.Map) []string {
	var out []string
	add := func(v any) {
		switch x := v.(type) {
		case string:
			out = append(out, x)
		case []any:
			for _, a := range x {
				s, _ := a.(string)
				out = append(out, s)
			}
		}
	}
	v, _ := e.Get("command")
	add(v)
	if !c.CommandArray {
		a, _ := e.Get("args")
		add(a)
	}
	return out
}

// ours reports whether e runs a ctxtra binary's MCP proxy.
func (c Config) ours(e *jsonorder.Map) bool {
	cl := c.commandLine(e)
	return len(cl) >= 3 && cl[1] == "mcp" && cl[2] == "proxy"
}

// binary is the program the entry runs, "" without one.
func (c Config) binary(e *jsonorder.Map) string {
	if cl := c.commandLine(e); len(cl) > 0 {
		return cl[0]
	}
	return ""
}

// MCPStatus describes the entry, with CheckMCP's notices.
func (c Config) MCPStatus(bin string, o agent.MCPCheck) agent.Status {
	data, err := c.load()
	if err != nil {
		return agent.Status{State: "unreadable: " + err.Error()}
	}
	notices := c.check(data, bin, o)
	servers, _ := c.servers(data, false)
	cur, has := entry(servers)
	if servers == nil || !has || cur == nil || !c.ours(cur) {
		return agent.Status{State: "no MCP entry", OK: len(notices) == 0, Details: notices}
	}
	s := agent.Status{State: fmt.Sprintf("MCP entry runs %s", paths.Tilde(c.binary(cur))), OK: len(notices) == 0, Details: notices}
	if _, ok := agent.CheckBinary(c.binary(cur)); ok {
		s.State += " (ok)"
	}
	return s
}

// CheckMCP returns the notices about the entry.
func (c Config) CheckMCP(bin string, o agent.MCPCheck) []string {
	data, err := c.load()
	if err != nil {
		if !o.Want && !o.Known {
			return nil
		}
		return []string{agent.UnreadableNotice(c.Path, err)}
	}
	return c.check(data, bin, o)
}

func (c Config) check(data *jsonorder.Map, bin string, o agent.MCPCheck) []string {
	repair := agent.Repair(c.Agent)
	servers, err := c.servers(data, false)
	if err != nil {
		if !o.Want && !o.Known {
			return nil
		}
		return []string{err.Error()}
	}
	cur, has := entry(servers)
	switch {
	case has && (cur == nil || !c.ours(cur)):
		if !o.Want {
			return nil
		}
		return []string{fmt.Sprintf("%s: %s; rename or delete it, then %s", paths.Tilde(c.Path), handEdited, repair)}
	case !has && o.Known:
		return []string{agent.GoneNotice("the ctxtra MCP server is", c.Path, c.Agent, repair)}
	case !has && o.Want:
		return []string{"the ctxtra MCP server is missing; " + repair}
	case !has:
		return nil
	}
	out := agent.BinaryNotices("the ctxtra MCP server runs", []string{c.binary(cur)}, bin, repair)
	if !slices.Equal(c.commandLine(cur)[1:], agent.MCPArgs(c.Agent)) {
		out = append(out, fmt.Sprintf("the ctxtra MCP server doesn't run as %s; %s", c.Agent, repair))
	}
	return out
}

// MCPCommand is the program and arguments of ctxtra's entry.
func (c Config) MCPCommand() ([]string, bool) {
	data, err := c.load()
	if err != nil {
		return nil, false
	}
	servers, err := c.servers(data, false)
	if err != nil {
		return nil, false
	}
	cur, _ := entry(servers)
	if cur == nil || !c.ours(cur) {
		return nil, false
	}
	return c.commandLine(cur), true
}
