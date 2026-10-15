// Package agent defines what ctxtra needs from a supported coding agent:
// how to set up its hooks, how to check them, and the format of the hook
// answer. Each agent is implemented in its own package under internal/agent;
// internal/agent/registry lists them.
package agent

import (
	"fmt"
	"path/filepath"
	"strings"

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
	// PreviewLimit is the size of hook output, in characters, above which the
	// agent shows the model only a preview; 0 when there is no known limit.
	PreviewLimit() int
}

// AdditionalContext is the hook answer format that Claude Code and Codex CLI
// share: the text as additionalContext for the event. At session start,
// systemMessage also shows the user a one-line Summary; subagents don't get
// one, it would repeat for every subagent.
func AdditionalContext(event string, res *resolve.Result) any {
	out := map[string]any{}
	if text := res.Render(); text != "" {
		out["hookSpecificOutput"] = map[string]any{"hookEventName": event, "additionalContext": text}
	}
	if s := Summary(res); s != "" && event == "SessionStart" {
		out["systemMessage"] = s
	}
	return out
}

// summaryNames is how many names Summary lists per kind before "+N more".
const summaryNames = 3

// Summary is a one-line message for the user about what the session got:
// files by base name and skills by name. It is "" when ctxtra added
// nothing and found no problem.
func Summary(res *resolve.Result) string {
	if res == nil {
		return ""
	}
	var added, issues []string
	if n := len(res.Files); n > 0 {
		names := make([]string, n)
		for i, h := range res.Files {
			names[i] = filepath.Base(h.Path)
		}
		added = append(added, plural(n, "file")+" "+nameList(names))
	}
	if n := len(res.Skills); n > 0 {
		names := make([]string, n)
		for i, h := range res.Skills {
			names[i] = h.Name
		}
		added = append(added, plural(n, "skill")+" "+nameList(names))
	}
	if n := len(res.Missing); n > 0 {
		issues = append(issues, fmt.Sprintf("%d not found", n))
	}
	if n := len(res.Dropped); n > 0 {
		issues = append(issues, fmt.Sprintf("%d over budget", n))
	}
	if n := len(res.Problems); n > 0 {
		issues = append(issues, plural(n, "problem"))
	}
	if len(added) == 0 && len(issues) == 0 {
		return ""
	}
	s := "ctxtra: added nothing"
	if len(added) > 0 {
		s = "ctxtra: added " + strings.Join(added, " and ")
	}
	if len(issues) > 0 {
		s += "; " + strings.Join(issues, ", ") + " (run ctxtra doctor)"
	}
	return s
}

// nameList is "(a, b, c)", or "(a, b, c, +2 more)" for more than
// summaryNames names.
func nameList(names []string) string {
	if len(names) > summaryNames {
		names = append(names[:summaryNames:summaryNames], fmt.Sprintf("+%d more", len(names)-summaryNames))
	}
	return "(" + strings.Join(names, ", ") + ")"
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
