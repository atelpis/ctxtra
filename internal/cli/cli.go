// Package cli implements the ctxtra commands.
package cli

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/atelpis/ctxtra/internal/agent/registry"
	"github.com/atelpis/ctxtra/internal/hook"
	"github.com/atelpis/ctxtra/internal/setup"
	"github.com/atelpis/ctxtra/internal/term"
)

// IO is where commands read and write.
type IO struct {
	In       io.Reader
	Out, Err io.Writer
}

var usage = fmt.Sprintf(`ctxtra: add your instruction files and skills to agent sessions (preview)

Usage:
  ctxtra setup [--agent A]... [--remove] [--dry-run]
                          add hooks that run this binary to your user-level agent configs;
                          --remove removes only ctxtra's entries
  ctxtra add <git URL> [<binding file in the repo>] [--as NAME] [--branch BRANCH]
                          add a team repository as a pinned source; its files change
                          only when you accept a new commit with sync
  ctxtra add <binding file> [--trust] [--as NAME]
                          add one of your binding files; --trust adds its repository
                          as a trusted source (read from disk, changes apply right away)
  ctxtra add --trust <folder>  add a folder of your own files as a trusted source
  ctxtra add ctxtra://<source>/<binding file>
                          add a binding file from a source you have
  ctxtra sync [--full] [SOURCE]...
                          fetch pinned sources, show what changed, and accept it;
                          --full shows changed files you don't use in full too
  ctxtra sync --restore [SOURCE]...
                          fetch exactly the commits in the config (a new laptop, a lost
                          store); changes nothing in the config
  ctxtra files [DIR]      files and skills a session in DIR gets (default: .)
  ctxtra show [DIR]       the exact text a session in DIR gets
  ctxtra explain [DIR]    why each binding matches or not
  ctxtra doctor [DIR]     config problems, setup state, Codex trust, recent hook runs
  ctxtra hook AGENT [EVENT]  hook entry point; reads the agent's hook JSON on stdin
  ctxtra version

Agents: %s. Without --agent: every agent found on this machine.
Config: $CTXTRA_CONFIG or ~/.config/ctxtra/config.yaml (see docs/spec.md).
`, strings.Join(registry.Names(), ", "))

// Run runs one command and returns the exit code.
func Run(args []string, stdio IO, version string) int {
	if len(args) == 0 {
		fmt.Fprint(stdio.Out, usage)
		return 0
	}
	cmd, rest := args[0], args[1:]
	if cmd != "hook" {
		// Output can hold text from sources (selectors, paths, diffs): no
		// control characters reach the terminal. The hook's stdout is JSON
		// for the agent and stays as it is.
		stdio.Out, stdio.Err = term.Writer{W: stdio.Out}, term.Writer{W: stdio.Err}
	}
	switch cmd {
	case "-h", "--help", "help":
		fmt.Fprint(stdio.Out, usage)
		return 0
	case "version":
		fmt.Fprintln(stdio.Out, version)
		return 0
	case "hook": // never fails, never parses flags
		if len(rest) == 0 {
			fmt.Fprintln(stdio.Out, "{}")
			return 0
		}
		event := "SessionStart"
		if len(rest) > 1 {
			event = rest[1]
		}
		return hook.Run(rest[0], event, hookInput(stdio.In), stdio.Out, term.Writer{W: stdio.Err})
	case "setup":
		return runSetup(rest, stdio)
	case "add":
		return runAdd(rest, stdio)
	case "sync":
		return runSync(rest, stdio)
	case "files", "show", "explain", "doctor":
		fs := flag.NewFlagSet(cmd, flag.ContinueOnError)
		fs.SetOutput(stdio.Err)
		if err := fs.Parse(rest); err != nil {
			return parseError(err)
		}
		if fs.NArg() > 1 {
			fmt.Fprintf(stdio.Err, "ctxtra %s: expected at most one folder\n", cmd)
			return 2
		}
		dir := fs.Arg(0)
		if dir == "" {
			dir = "."
		}
		r := report{stdio}
		switch cmd {
		case "files":
			return r.files(dir)
		case "show":
			return r.show(dir)
		case "explain":
			return r.explain(dir)
		default:
			return r.doctor(dir)
		}
	}
	fmt.Fprintf(stdio.Err, "unknown command %q\n\n%s", cmd, usage)
	return 2
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

// listFlag is a flag that may be repeated.
type listFlag []string

func (l *listFlag) String() string     { return strings.Join(*l, ",") }
func (l *listFlag) Set(v string) error { *l = append(*l, v); return nil }

func runSetup(args []string, stdio IO) int {
	fs := flag.NewFlagSet("setup", flag.ContinueOnError)
	fs.SetOutput(stdio.Err)
	var agents listFlag
	fs.Var(&agents, "agent", "agent to change (repeatable): claude, codex, pi, opencode, cursor")
	remove := fs.Bool("remove", false, "remove ctxtra's hooks")
	dryRun := fs.Bool("dry-run", false, "show what would change")
	if err := fs.Parse(args); err != nil {
		return parseError(err)
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(stdio.Err, "ctxtra setup: unexpected argument %q\n", fs.Arg(0))
		return 2
	}
	o := setup.Options{Agents: agents}
	o.Remove, o.DryRun = *remove, *dryRun
	return setupCmd(stdio, o)
}

// parseError is the exit code for a flag error: 0 for -h, 2 otherwise.
func parseError(err error) int {
	if errors.Is(err, flag.ErrHelp) {
		return 0
	}
	return 2
}
