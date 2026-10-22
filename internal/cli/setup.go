package cli

import (
	"bufio"
	"fmt"
	"strings"

	"github.com/atelpis/ctxtra/internal/paths"
	"github.com/atelpis/ctxtra/internal/setup"
	"github.com/atelpis/ctxtra/internal/state"
	"github.com/atelpis/ctxtra/internal/term"
)

// binary finds the binary the hooks run; a variable so tests can replace it.
var binary = setup.Binary

func setupCmd(stdio IO, o setup.Options) int {
	if !o.Remove {
		bin, warnings, err := binary()
		if err != nil {
			fmt.Fprintf(stdio.Err, "ctxtra setup: %v\n", err)
			return 1
		}
		o.Binary = bin
		for _, w := range warnings {
			fmt.Fprintln(stdio.Err, "warning:", w)
		}
	}
	r, err := setup.Run(o)
	if err != nil {
		fmt.Fprintf(stdio.Err, "ctxtra setup: %v\n", err)
		return 1
	}
	setupReport(stdio, o, r)
	for _, w := range r.Warnings {
		term.Fprintf(stdio.Err, term.Yellow, "warning: %s\n", w)
	}
	for _, n := range r.Notes {
		fmt.Fprintln(stdio.Out)
		term.Fprintf(stdio.Out, term.Yellow, "%s\n", n)
	}
	for _, e := range r.Errors {
		term.Fprintf(stdio.Err, term.Red, "%s\n", e)
	}
	if r.Record != nil {
		fmt.Fprintf(stdio.Err, "ctxtra setup: can't write %s: %v; the changes above stay, and the next setup writes it\n",
			paths.Tilde(state.SetupsPath()), r.Record)
		return 1
	}
	if len(r.Errors) > 0 {
		return 1
	}
	if !o.Remove && !o.DryRun {
		// One reader for every question: a reader of its own would take
		// the input of the next.
		in := bufio.NewReader(stdio.In)
		if err := offerSkills(stdio, in); err != nil {
			fmt.Fprintf(stdio.Err, "ctxtra skill install: %v\n", err)
			return 1
		}
		return offerImport(stdio, in)
	}
	return 0
}

// setupReport says what setup did for the agents without an error, and
// lists the files it changed, by agent.
func setupReport(stdio IO, o setup.Options, r setup.Report) {
	var ok []string
	width := 0
	for _, a := range r.Agents {
		title := a.Delivery().Title
		if a.Err == nil {
			ok = append(ok, title)
		}
		if len(a.Changes) > 0 {
			width = max(width, len(title))
		}
	}
	agents := and(ok)
	changed := len(r.Changes) > 0
	var head string
	switch {
	case len(r.Agents) == 0:
		head = "ctxtra found no agents to set up."
	case o.DryRun && changed:
		head = "Setup would change these files:"
	case len(ok) == 0:
	case o.DryRun:
		head = "Nothing to change for " + agents + "."
	case o.Remove && changed:
		head = "ctxtra is removed from " + agents + ":"
	case o.Remove:
		head = "ctxtra isn't set up for " + agents + "."
	case changed:
		head = "ctxtra is set up for " + agents + ":"
	default:
		head = "ctxtra is already set up for " + agents + "."
	}
	if head != "" {
		term.Fprintf(stdio.Out, term.Bold, "%s\n", head)
	}
	for _, a := range r.Agents {
		title := a.Delivery().Title
		for _, c := range a.Changes {
			fmt.Fprintf(stdio.Out, "  %-*s   %s\n", width, title, paths.Tilde(c))
			title = ""
		}
	}
}

// and is "a", "a and b", or "a, b and c".
func and(names []string) string {
	if len(names) < 2 {
		return strings.Join(names, "")
	}
	return strings.Join(names[:len(names)-1], ", ") + " and " + names[len(names)-1]
}
