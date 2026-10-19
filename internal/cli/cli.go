// Package cli implements the ctxtra commands.
package cli

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"slices"
	"strconv"
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
                          include one of your binding files; --trust adds its repository
                          as a trusted source (read from disk, changes apply right away)
  ctxtra add --trust <folder>  add a folder of your own files as a trusted source
  ctxtra add --local-git <clone> [--as NAME]
                          add your clone of a team repository as a pinned source that
                          follows its committed HEAD, to try changes before you push
  ctxtra bind NAME (--global | --dir PATTERN... | --repo REMOTE...) [--file FILE]... [--skill FOLDER]...
               [--dry-run]
                          add a binding to the config; it is written only when its
                          files exist and are trusted; --dry-run only checks it
  ctxtra remove <source>|<include entry>
                          remove a source with its include entries, or one entry
  ctxtra add ctxtra://<source>/<binding file>
                          include a binding file from a source you have
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
  ctxtra skill [install]  show the ctxtra skill, which lets your agents set up and
                          change ctxtra; install adds it to every session
  ctxtra help [TOPIC]     this text, or a topic of the docs of this version
  ctxtra hook AGENT [EVENT [--slot K --of N]]
                          hook entry point; reads the agent's hook JSON on stdin
  ctxtra version

Agents: %s. Without --agent: every agent found on this machine.
Config: $CTXTRA_CONFIG or ~/.config/ctxtra/config.yaml.

Help topics (ctxtra help TOPIC):
%s`, strings.Join(registry.Names(), ", "), helpTopics())

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
	notices := !slices.Contains(quiet, cmd)
	defer func() {
		if notices {
			setupNotices(stdio.Err)
		}
	}()
	switch cmd {
	case "-h", "--help":
		fmt.Fprint(stdio.Out, usage)
		return 0
	case "help":
		return runHelp(rest, stdio)
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
		args := hook.Args{Slot: -1}
		if len(rest) > 2 {
			args = hookArgs(rest[2:])
		}
		return hook.RunArgs(rest[0], event, args, hookInput(stdio.In), stdio.Out, term.Writer{W: stdio.Err})
	case "setup":
		return runSetup(rest, stdio)
	case "add":
		return runAdd(rest, stdio)
	case "sync":
		return runSync(rest, stdio)
	case "bind":
		return runBind(rest, stdio)
	case "remove":
		return runRemove(rest, stdio)
	case "skill":
		return runSkill(rest, stdio)
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
	notices = false
	fmt.Fprintf(stdio.Err, "unknown command %q\n\n%s", cmd, usage)
	return 2
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

// listFlag is a flag that may be repeated.
type listFlag []string

func (l *listFlag) String() string     { return strings.Join(*l, ",") }
func (l *listFlag) Set(v string) error { *l = append(*l, v); return nil }

func runSetup(args []string, stdio IO) int {
	fs := flag.NewFlagSet("setup", flag.ContinueOnError)
	fs.SetOutput(stdio.Err)
	var agents listFlag
	fs.Var(&agents, "agent", "agent to change (repeatable): claude, codex, pi, opencode, muse, hermes, dsh, cursor, copilot, antigravity, devin")
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
