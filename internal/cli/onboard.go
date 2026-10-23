package cli

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/atelpis/ctxtra/internal/agent"
	"github.com/atelpis/ctxtra/internal/setup"
	"github.com/atelpis/ctxtra/internal/skill"
	"github.com/atelpis/ctxtra/internal/term"
)

func onboard(stdio IO, agents []string, prompts bool) int {
	if !prompts {
		fmt.Fprintln(stdio.Err, "ctxtra onboard: needs an interactive terminal without --no-input; use ctxtra setup, ctxtra skill install and ctxtra import separately")
		return 1
	}
	term.Fprintf(stdio.Out, term.Bold, "Let's get started with ctxtra\n")
	fmt.Fprintln(stdio.Out, "First, we'll add hooks to your agents' user-level configs so new sessions get your instructions and skills. Each changed file is backed up first.")
	if code := setupHooks(stdio, setup.Options{Agents: agents, Options: agent.Options{DryRun: true}}); code != 0 {
		return code
	}
	// Share the reader so buffered answers aren't lost between steps.
	in := bufio.NewReader(stdio.In)
	term.Fprintf(stdio.Err, term.Bold, "Install hooks and continue? [Y/n] ")
	ok, err := onboardConfirm(in)
	if err != nil {
		fmt.Fprintf(stdio.Err, "ctxtra onboard: %v\n", err)
		return 1
	}
	if !ok {
		fmt.Fprintln(stdio.Out, "Onboarding stopped. Hooks are required to continue. To try again: ctxtra onboard")
		return 0
	}
	fmt.Fprintln(stdio.Out)
	if code := setupHooks(stdio, setup.Options{Agents: agents}); code != 0 {
		return code
	}
	if _, _, err := importCandidates(); err != nil {
		fmt.Fprintf(stdio.Err, "ctxtra onboard: %v\n", err)
		return 1
	}
	for _, offer := range []struct{ name, title, about string }{
		{skill.Base, "Your agents can set up ctxtra for you",
			"ctxtra comes with a skill that makes getting started easy. Your agents can create your bindings, change your config, and bring the instructions you already have into ctxtra."},
		{follow, "Work across multiple projects in one session?",
			"The ctxtra-follow skill helps your agent pick up the instructions and skills you've configured for each project as your work moves between them."},
	} {
		if skillBound(offer.name) != nil {
			continue
		}
		onboardSeparator(stdio.Out)
		term.Fprintf(stdio.Out, term.Bold, "%s\n", offer.title)
		fmt.Fprintf(stdio.Out, "%s\n\n", offer.about)
		term.Fprintf(stdio.Err, term.Bold, "Install the %s skill? [Y/n] ", offer.name)
		ok, err := onboardConfirm(in)
		if err != nil {
			fmt.Fprintf(stdio.Err, "ctxtra onboard: %v\n", err)
			return 1
		}
		if !ok {
			fmt.Fprintf(stdio.Out, "Skipped. To install it later: ctxtra skill install %s\n", offer.name)
			continue
		}
		fmt.Fprintln(stdio.Out)
		if code := skillInstall(stdio, offer.name, false); code != 0 {
			return code
		}
	}
	return onboardImport(stdio, in)
}

func onboardImport(stdio IO, in *bufio.Reader) int {
	if hasInstructions() {
		return 0
	}
	files, launchers, err := importCandidates()
	if err != nil {
		fmt.Fprintf(stdio.Err, "ctxtra onboard: %v\n", err)
		return 1
	}
	onboardSeparator(stdio.Out)
	if len(files) == 0 {
		fmt.Fprintln(stdio.Out, "Your agents have no existing instruction files to import. New agent sessions will use ctxtra.")
		return 0
	}
	term.Fprintf(stdio.Out, term.Bold, "Your agents have instruction files of their own:\n")
	for _, f := range files {
		fmt.Fprintf(stdio.Out, "  %s\n", f)
	}
	if unreadable(stdio, files) {
		return 1
	}
	if skillBound(skill.Base) == nil {
		fmt.Fprintln(stdio.Out, "Agent-assisted import needs the ctxtra skill. To install it later: ctxtra skill install ctxtra")
	}
	if skillBound(skill.Base) == nil || len(launchers) == 0 {
		fmt.Fprintln(stdio.Out, "To bring these into ctxtra later, run ctxtra import.")
		return 0
	}
	fmt.Fprint(stdio.Out, `An agent can move them into ctxtra, so that all your agents get them: it merges
the rules that repeat and shows you the result. ctxtra backs the files up
first; the agent empties them only if you agree.
`)
	term.Fprintf(stdio.Err, term.Bold, "Start an agent to help with this now? [Y/n] ")
	ok, err := onboardConfirm(in)
	if err != nil {
		fmt.Fprintf(stdio.Err, "ctxtra onboard: %v\n", err)
		return 1
	}
	if !ok {
		fmt.Fprintln(stdio.Out, "Not started. To do it later: ctxtra import")
		return 0
	}
	chosen := 0
	if len(launchers) > 1 {
		term.Fprintf(stdio.Err, term.Bold, "Which agent?\n")
		for i, a := range launchers {
			fmt.Fprintf(stdio.Err, "  %d) %s\n", i+1, a.Delivery().Title)
		}
		term.Fprintf(stdio.Err, term.Bold, "Choose 1-%d [1]: ", len(launchers))
		answer, err := in.ReadString('\n')
		if err != nil {
			if !errors.Is(err, io.EOF) {
				fmt.Fprintf(stdio.Err, "ctxtra onboard: %v\n", err)
				return 1
			}
			fmt.Fprintln(stdio.Out, "Not started. To do it later: ctxtra import")
			return 0
		}
		if answer = strings.TrimSpace(answer); answer != "" {
			n, err := strconv.Atoi(answer)
			if err != nil || n < 1 || n > len(launchers) {
				fmt.Fprintln(stdio.Err, "ctxtra onboard: invalid agent choice; to try again, run ctxtra onboard")
				return 1
			}
			chosen = n - 1
		}
	}
	return runImport(stdio, launchers[chosen].Name(), false, true)
}

// EOF is not consent, even if it follows an unfinished answer.
func onboardConfirm(in *bufio.Reader) (bool, error) {
	answer, err := in.ReadString('\n')
	if errors.Is(err, io.EOF) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	answer = strings.ToLower(strings.TrimSpace(answer))
	return answer == "" || answer == "y" || answer == "yes", nil
}

func onboardSeparator(out io.Writer) {
	fmt.Fprintln(out, "\n------------------------------------------------------------")
	fmt.Fprintln(out)
}
