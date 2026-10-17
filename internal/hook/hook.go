// Package hook is the entry point the agents run at session start. It never
// breaks a session: on any error it injects nothing and logs the error.
package hook

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/atelpis/ctxtra/internal/agent"
	"github.com/atelpis/ctxtra/internal/agent/registry"
	"github.com/atelpis/ctxtra/internal/jsonorder"
	"github.com/atelpis/ctxtra/internal/paths"
	"github.com/atelpis/ctxtra/internal/resolve"
)

const maxInput = 10 << 20

// payload is the part of the agents' hook input that ctxtra uses.
type payload struct {
	Cwd            string   `json:"cwd"`
	WorkspaceRoots []string `json:"workspace_roots"` // Cursor
	WorkspacePaths []string `json:"workspacePaths"`  // Antigravity
	Source         string   `json:"source"`
	AgentType      string   `json:"agent_type"`
}

// Run reads the hook input from in and writes the agent's answer to out. It
// always returns 0. An unknown agent gets "{}".
func Run(harness, event string, in io.Reader, out, errOut io.Writer) (code int) {
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
	res, _ := resolve.Session(dir, paths.ConfigPath())
	e := newEntry(ts, harness, event, p, res, resolve.Chars(res.Render()))
	if s, ok := a.(agent.Skipper); ok {
		if e.Skipped = s.Skip(); e.Skipped != "" {
			e.Chars = 0
			logEntry(e)
			fmt.Fprintln(out, empty)
			return 0
		}
	}
	logEntry(e)
	for _, m := range res.Missing {
		fmt.Fprintf(errOut, "ctxtra: bound file not found: %s\n", m)
	}
	b, err := jsonorder.Marshal(a.Answer(event, res))
	if err != nil {
		panic(err)
	}
	fmt.Fprintln(out, string(b))
	return 0
}
