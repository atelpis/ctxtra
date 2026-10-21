// Package driver runs a coding agent non-interactively against the mock
// model, in a fixture's home folder. Each supported agent has one Driver.
//
// To add an agent: implement Driver in a new file, add it to All, and pin
// its version in agents/package.json. See ../README.md.
package driver

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

// Cap is something an agent can do. A scenario that needs a capability runs
// only for drivers that declare it; for those, a failure is a failure, never
// a skip.
type Cap string

const (
	// Subagent: the agent can start a subagent (mock.StartSubagent), and
	// ctxtra's hook runs for it.
	Subagent Cap = "subagent"
	// UserMessage: the run's output shows the message ctxtra's hook shows
	// the user at session start.
	UserMessage Cap = "user-message"
	// Untrusted: the agent can run with hooks that the user hasn't trusted,
	// which must then add nothing.
	Untrusted Cap = "untrusted"
	// SkillTool: the agent loads a skill by name with a skill tool
	// (mock.LoadSkill), and its skill list doesn't show the SKILL.md path.
	SkillTool Cap = "skill-tool"
	// Resume: the agent can continue its last session non-interactively
	// (Options.Resume).
	Resume Cap = "resume"
)

// Driver is one agent.
type Driver interface {
	// Name is the agent's name in ctxtra (setup --agent NAME).
	Name() string
	// Executable is the agent's command name, as found in PATH.
	Executable() string
	// Caps lists what the agent supports, see Cap.
	Caps() []Cap
	// Command is the agent's command for one run: a prompt in dir, with the
	// model at mockURL (without /v1). It may write the agent's config files
	// into home. env is the fixture's environment; the result replaces it.
	Command(bin, home, dir, mockURL, prompt string, opts Options, env []string) (*exec.Cmd, error)
	// UserMessages are the messages for the user that hooks added, read from
	// the run's output or from the agent's session log in home. Only called
	// for drivers with UserMessage.
	UserMessages(home, stdout string) []string
}

// TUI is a driver that also runs the agent's interactive terminal app, as
// ctxtra import starts it: a shim in PATH runs bin with Pre, the arguments
// ctxtra launches with, and Post, in Env.
type TUI interface {
	// Interactive is the app against the mock at mockURL. It may write the
	// agent's config files into home. env is the fixture's environment.
	Interactive(bin, home, mockURL string, env []string) (Interactive, error)
	// Answer is the keys (tmux send-keys arguments) that answer a startup
	// question on the screen, or nil when there is none.
	Answer(screen string) []string
}

// Interactive is the command line and environment of an interactive app.
type Interactive struct {
	Pre, Post, Env []string
}

// Options change one run.
type Options struct {
	// Prompt replaces mock.Prompt as the task.
	Prompt string
	// Untrusted runs Codex without bypassing hook trust.
	Untrusted bool
	// Resume continues the agent's last session.
	Resume bool
}

// All is every driver.
var All = []Driver{Claude{}, Codex{}, Pi{}, OpenCode{}, Copilot{}, Muse{}, Antigravity{}, Hermes{}, DSH{}}

// Has reports whether d declares c.
func Has(d Driver, c Cap) bool {
	for _, x := range d.Caps() {
		if x == c {
			return true
		}
	}
	return false
}

// Binary finds the agent's executable: $CTXTRA_E2E_<NAME> if set, then the
// pinned version installed by agents/install.sh, then PATH.
func Binary(d Driver) (string, error) {
	exe := d.Executable()
	env := "CTXTRA_E2E_" + strings.ToUpper(d.Name())
	if p := os.Getenv(env); p != "" {
		return p, nil
	}
	if p, ok := pinned(exe); ok {
		return p, nil
	}
	p, err := exec.LookPath(exe)
	if err != nil {
		return "", fmt.Errorf("%s not found: install it, run agents/install.sh, or set %s", exe, env)
	}
	return p, nil
}

// Version is the first line of `bin --version`.
func Version(bin string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, bin, "--version").Output()
	if err != nil {
		return "unknown (" + err.Error() + ")"
	}
	v, _, _ := strings.Cut(strings.TrimSpace(string(out)), "\n")
	return v
}
