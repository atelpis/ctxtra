// Package cli implements the ctxtra commands.
package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/atelpis/ctxtra/internal/hook"
	"github.com/atelpis/ctxtra/internal/paths"
	"github.com/atelpis/ctxtra/internal/source"
	"github.com/atelpis/ctxtra/internal/term"
)

// IO is where commands read and write.
type IO struct {
	In       io.Reader
	Out, Err io.Writer
}

// app holds what one run of ctxtra shares between its commands: the
// global flags and the command that ran.
type app struct {
	IO
	version string
	config  string // --config
	noInput bool   // --no-input
	ran     *cobra.Command
}

// exitCode ends a command that has said what went wrong.
type exitCode int

func (e exitCode) Error() string { return "exit status " + strconv.Itoa(int(e)) }

// code is the error for an exit code: nil for 0.
func code(n int) error {
	if n == 0 {
		return nil
	}
	return exitCode(n)
}

// Run runs one command and returns the exit code: 0 for success, 1 when
// the command failed, 2 for a usage error.
func Run(args []string, stdio IO, version string) int {
	if len(args) > 0 && args[0] == "hook" {
		return runHook(args[1:], stdio)
	}
	// Output can hold text from sources (selectors, paths, diffs): no
	// control characters reach the terminal. The hook's stdout is JSON
	// for the agent and stays as it is.
	stdio.Out, stdio.Err = term.Writer{W: stdio.Out}, term.Writer{W: stdio.Err}
	// --config sets $CTXTRA_CONFIG for this run, and for the agent that
	// import starts.
	if old, ok := os.LookupEnv("CTXTRA_CONFIG"); ok {
		defer os.Setenv("CTXTRA_CONFIG", old)
	} else {
		defer os.Unsetenv("CTXTRA_CONFIG")
	}
	a := &app{IO: stdio, version: version}
	root := a.root()
	root.SetArgs(args)
	cmd, err := root.ExecuteC()
	n := 0
	var ec exitCode
	switch {
	case errors.As(err, &ec):
		n = int(ec)
	case err != nil:
		path := cmd.CommandPath()
		fmt.Fprintf(stdio.Err, "%s: %s\n", path, strings.TrimRight(err.Error(), "\n"))
		use := cmd.UseLine()
		if !cmd.HasParent() {
			use = "ctxtra COMMAND [flags]"
		}
		fmt.Fprintf(stdio.Err, "Usage: %s\n", use)
		fmt.Fprintf(stdio.Err, "Run '%s --help' for more.\n", path)
		n = 2
	}
	if a.ran != nil && !slices.Contains(quiet, a.ran.Name()) {
		setupNotices(stdio.Err)
	}
	return n
}

// run makes f a cobra RunE, and records that the command ran. f returns
// an exitCode after it has said what went wrong, and any other error for a
// usage error.
func (a *app) run(f func(cmd *cobra.Command, args []string) error) func(*cobra.Command, []string) error {
	return func(cmd *cobra.Command, args []string) error {
		a.ran = cmd
		return f(cmd, args)
	}
}

// global applies the global flags before a command runs.
func (a *app) global(cmd *cobra.Command, _ []string) error {
	source.NoPrompt = !a.prompts()
	if a.config == "" {
		return nil
	}
	if cmd.Name() == "setup" || cmd.Name() == "onboard" {
		return errors.New("--config doesn't apply: the hooks don't carry a config path, sessions read $CTXTRA_CONFIG or ~/.config/ctxtra/config.yaml")
	}
	abs, err := filepath.Abs(paths.Expand(a.config))
	if err != nil {
		return err
	}
	return os.Setenv("CTXTRA_CONFIG", abs)
}

// Interactive reports whether answers can be asked: stdin and stdout are
// terminals, so the user sees what a question is about. A variable so tests
// can replace it.
var Interactive = func(stdio IO) bool { return isTerminal(stdio.In) && isTerminal(stdio.Out) }

func isTerminal(x any) bool {
	if w, ok := x.(term.Writer); ok {
		x = w.W
	}
	f, ok := x.(*os.File)
	return ok && term.IsTerminal(f)
}

// prompts reports whether this run may ask questions.
func (a *app) prompts() bool { return !a.noInput && Interactive(a.IO) }

// jsonDoc is the output of --json.
type jsonDoc struct {
	SchemaVersion int          `json:"schema_version"`
	Data          any          `json:"data"`
	Diagnostics   []diagnostic `json:"diagnostics"`
}

// diagnostic is a problem or a notice, in --json output.
type diagnostic struct {
	Severity string `json:"severity"` // error, warning or notice
	Message  string `json:"message"`
}

// diagnostics are messages of one severity.
func diagnostics(severity string, msgs []string) []diagnostic {
	out := make([]diagnostic, len(msgs))
	for i, m := range msgs {
		out[i] = diagnostic{severity, m}
	}
	return out
}

// writeJSON writes data as the one JSON document of the output. It goes
// around term.Writer: JSON escapes control characters itself.
func writeJSON(w io.Writer, data any, diags []diagnostic) {
	if t, ok := w.(term.Writer); ok {
		w = t.W
	}
	if diags == nil {
		diags = []diagnostic{}
	}
	e := json.NewEncoder(w)
	e.SetEscapeHTML(false)
	e.SetIndent("", "  ")
	e.Encode(jsonDoc{1, data, diags})
}

// runHook is ctxtra hook AGENT [EVENT [--slot K --of N]]. It never fails
// and parses no flags but its own, so that hooks that any version set up
// keep running.
func runHook(args []string, stdio IO) int {
	if len(args) == 0 {
		fmt.Fprintln(stdio.Out, "{}")
		return 0
	}
	event := "SessionStart"
	if len(args) > 1 {
		event = args[1]
	}
	h := hook.Args{Slot: -1}
	if len(args) > 2 {
		h = hookArgs(args[2:])
	}
	return hook.RunArgs(args[0], event, h, hookInput(stdio.In), stdio.Out, term.Writer{W: stdio.Err})
}

// hookArgs reads --slot K and --of N wherever they are, and ignores other
// arguments, so a hook that a later version set up, with options this
// version doesn't know, still runs. Options that can't be read are an
// error: run without its slot, each hook of a slotted agent would add the
// whole text.
func hookArgs(args []string) hook.Args {
	out := hook.Args{Slot: -1}
	seen := map[string]bool{}
	for i := 0; i < len(args); i++ {
		name := args[i]
		if name != "--slot" && name != "--of" {
			continue
		}
		if seen[name] {
			out.Err = fmt.Errorf("%s given twice", name)
			return out
		}
		seen[name] = true
		i++
		if i == len(args) {
			out.Err = fmt.Errorf("%s without a value", name)
			return out
		}
		n, err := strconv.Atoi(args[i])
		if err != nil || strings.Trim(args[i], "0123456789") != "" {
			out.Err = fmt.Errorf("%s %q is not a non-negative integer", name, args[i])
			return out
		}
		if name == "--slot" {
			out.Slot = n
		} else {
			out.Slots = n
		}
	}
	switch {
	case !seen["--of"]:
	case !seen["--slot"]:
		out.Err = errors.New("--of without --slot")
	case out.Slots == 0:
		out.Err = errors.New("--of 0")
	case out.Slot >= out.Slots:
		out.Err = fmt.Errorf("--slot %d not below --of %d", out.Slot, out.Slots)
	}
	return out
}

// hookInput doesn't read from a terminal: that would wait for input forever
// when someone runs the hook by hand.
func hookInput(in io.Reader) io.Reader {
	if f, ok := in.(*os.File); ok {
		if st, err := f.Stat(); err != nil || st.Mode()&os.ModeCharDevice != 0 {
			return strings.NewReader("")
		}
	}
	return in
}
