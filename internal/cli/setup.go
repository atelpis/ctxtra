package cli

import (
	"fmt"
	"strings"

	"github.com/atelpis/ctxtra/internal/paths"
	"github.com/atelpis/ctxtra/internal/setup"
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
	name := "setup"
	if o.Remove {
		name = "setup --remove"
	}
	verb := "nothing to change"
	if len(r.Changes) > 0 {
		verb = "changed"
		if o.DryRun {
			verb = "would change"
		}
	}
	fmt.Fprintf(stdio.Out, "ctxtra %s (%s): %s\n", name, strings.Join(r.Agents, ", "), verb)
	for _, c := range r.Changes {
		fmt.Fprintf(stdio.Out, "  %s\n", c)
	}
	if !o.Remove {
		fmt.Fprintf(stdio.Out, "hooks run %s\n", paths.Tilde(o.Binary))
	}
	for _, w := range r.Warnings {
		fmt.Fprintln(stdio.Err, "warning:", w)
	}
	for _, n := range r.Notes {
		fmt.Fprintln(stdio.Out, n)
	}
	for _, e := range r.Errors {
		fmt.Fprintln(stdio.Err, e)
	}
	if len(r.Errors) > 0 {
		return 1
	}
	return 0
}
