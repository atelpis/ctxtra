package cli

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/atelpis/ctxtra/internal/agent"
	"github.com/atelpis/ctxtra/internal/agent/registry"
	"github.com/atelpis/ctxtra/internal/config"
	"github.com/atelpis/ctxtra/internal/fsutil"
	"github.com/atelpis/ctxtra/internal/paths"
	"github.com/atelpis/ctxtra/internal/skill"
	"github.com/atelpis/ctxtra/internal/state"
	"github.com/atelpis/ctxtra/internal/term"
)

// importFile is a user-level instruction file of an agent.
type importFile struct {
	agent.NativeFile
	agent, title string // the agent's name and title
}

func (f importFile) String() string {
	s := quoted(paths.Tilde(f.Path)) + " (" + f.title
	if f.Note != "" {
		s += "; " + f.Note
	}
	if f.Err != nil {
		s += "; can't read it: " + f.Err.Error()
	}
	return s + ")"
}

// lookPath and startAgent are variables so tests can replace them.
var (
	lookPath   = exec.LookPath
	startAgent = runAgent
)

// runImport is ctxtra import: it backs up and lists the instruction files
// that the agents have of their own and, with start, starts that agent to
// move them into ctxtra with the ctxtra skill.
func runImport(stdio IO, start string, dryRun, prompts bool) int {
	files, launchers, err := importCandidates()
	if err != nil {
		fmt.Fprintf(stdio.Err, "ctxtra import: %v\n", err)
		return 1
	}
	// Checked before the backup: a start that can't happen backs up nothing.
	var chosen agent.Agent
	if start != "" {
		a, ok := registry.Get(start)
		_, launches := a.(agent.Launcher)
		switch {
		case !ok:
			fmt.Fprintf(stdio.Err, "ctxtra import: no agent %q; agents: %s\n", start, strings.Join(registry.Names(), ", "))
			return 2
		case !launches:
			fmt.Fprintf(stdio.Err, "ctxtra import: ctxtra can't start %s; give the prompt to it yourself (ctxtra import prints it)\n", start)
			return 1
		case !slices.Contains(launchers, a):
			fmt.Fprintf(stdio.Err, "ctxtra import: %s isn't set up, or its command isn't in PATH\n", start)
			return 1
		case !prompts:
			fmt.Fprintln(stdio.Err, "ctxtra import: --start needs a terminal (or leave out --no-input): the agent works with you there")
			return 1
		case skillBound(skill.Base) == nil:
			fmt.Fprintln(stdio.Err, "ctxtra import: the agent follows the ctxtra skill: install it first with ctxtra skill install ctxtra")
			return 1
		}
		chosen = a
	}
	if len(files) == 0 {
		fmt.Fprintln(stdio.Out, "the agents that are set up have no instruction files of their own")
		return 0
	}
	term.Fprintf(stdio.Out, term.Bold, "Your agents have instruction files of their own:\n")
	for _, f := range files {
		fmt.Fprintf(stdio.Out, "  %s\n", f)
	}
	if unreadable(stdio, files) {
		return 1
	}
	if dryRun {
		if chosen != nil {
			fmt.Fprintf(stdio.Out, "would back them up and start %s to move them into ctxtra\n", chosen.Delivery().Title)
		} else {
			fmt.Fprintln(stdio.Out, "would back them up")
		}
		fmt.Fprintln(stdio.Out, "nothing changed (--dry-run)")
		return 0
	}
	backup, err := backupFiles(files)
	if err != nil {
		fmt.Fprintf(stdio.Err, "ctxtra import: back up the files: %v\n", err)
		return 1
	}
	term.Fprintf(stdio.Out, term.Green, "backed up the files to %s\n", paths.Tilde(backup))
	if chosen == nil {
		return importNext(stdio, files, launchers, backup)
	}
	// The prompt names the skill's files: write them, also when they are gone.
	if _, err := skill.Write(skill.Base); err != nil {
		fmt.Fprintf(stdio.Err, "ctxtra import: write the ctxtra skill: %v\n", err)
		return 1
	}
	prompt := importPrompt(files, backup)
	argv := chosen.(agent.Launcher).Launch(prompt)
	dir := filepath.Join(paths.StateDir(), "import")
	fmt.Fprintf(stdio.Out, "starting %s in %s with this prompt:\n\n%s\n\n", chosen.Delivery().Title, paths.Tilde(dir), prompt)
	bin, err := lookPath(argv[0])
	if err == nil {
		err = os.MkdirAll(dir, 0o700)
	}
	if err == nil {
		err = startAgent(bin, argv[1:], dir)
	}
	if err != nil {
		fmt.Fprintf(stdio.Err, "ctxtra import: %s: %v; you can give the prompt above to an agent\n", chosen.Delivery().Title, err)
		return 1
	}
	return 0
}

// importNext says, after a backup without --start, how the files get into
// ctxtra: the command that starts an agent for it, or, when ctxtra can
// start none, the prompt to give one.
func importNext(stdio IO, files []importFile, launchers []agent.Agent, backup string) int {
	fmt.Fprint(stdio.Out, `Not imported yet. An agent can move them into ctxtra, so that all your agents
get them: it merges the rules that repeat and shows you the result. It empties
the files only if you agree.
`)
	if skillBound(skill.Base) == nil {
		fmt.Fprintln(stdio.Out, "The agent follows the ctxtra skill; install it first: ctxtra skill install ctxtra")
	}
	if len(launchers) > 0 {
		var cmds []string
		for _, a := range launchers {
			cmds = append(cmds, "ctxtra import --start "+a.Name())
		}
		fmt.Fprintf(stdio.Out, "To start one: %s\n", strings.Join(cmds, ", or "))
		return 0
	}
	if skillBound(skill.Base) != nil {
		if _, err := skill.Write(skill.Base); err != nil {
			fmt.Fprintf(stdio.Err, "ctxtra import: write the ctxtra skill: %v\n", err)
			return 1
		}
		fmt.Fprintf(stdio.Out, "Start an agent and give it this prompt:\n\n%s\n", importPrompt(files, backup))
	}
	return 0
}

// importCandidates are the instruction files of the agents that setup.json
// lists as set up, also by an earlier setup, and those of them whose CLI is
// in PATH: only their sessions get the skill.
func importCandidates() ([]importFile, []agent.Agent, error) {
	setups, _, err := state.LoadSetups()
	if err != nil {
		return nil, nil, err
	}
	var files []importFile
	var launchers []agent.Agent
	set := 0
	for _, a := range registry.All() {
		if !setups.Known(a.Name(), a.ConfigPath()) {
			continue
		}
		set++
		if n, ok := a.(agent.Native); ok {
			for _, f := range n.NativeFiles() {
				// The agent that moves them runs in another folder.
				if abs, err := filepath.Abs(f.Path); err == nil {
					f.Path = abs
				}
				files = append(files, importFile{f, a.Name(), a.Delivery().Title})
			}
		}
		if l, ok := a.(agent.Launcher); ok {
			if _, err := lookPath(l.Launch("")[0]); err == nil {
				launchers = append(launchers, a)
			}
		}
	}
	if set == 0 {
		return nil, nil, errors.New("no agent is set up: run ctxtra setup first")
	}
	return files, launchers, nil
}

// unreadable reports, and says, whether some of the files can't be read:
// ctxtra can't back them up, and they would be left out.
func unreadable(stdio IO, files []importFile) bool {
	if !slices.ContainsFunc(files, func(f importFile) bool { return f.Err != nil }) {
		return false
	}
	fmt.Fprintln(stdio.Err, "ctxtra import: can't read some of the files above: fix or move them, then run ctxtra import again")
	return true
}

// hasInstructions reports whether a binding of the config adds instruction
// files.
func hasInstructions() bool {
	cfg := config.Load(paths.ConfigPath(), fsutil.NewReader(config.MaxFileBytes, config.MaxSessionBytes))
	return slices.ContainsFunc(cfg.Bindings, func(b config.Binding) bool { return len(b.Instructions) > 0 })
}

// backupFiles copies the files to a new folder in paths.BackupDir, each in
// a folder of its agent at its path under the home folder, and returns the
// folder.
func backupFiles(files []importFile) (string, error) {
	if err := os.MkdirAll(paths.BackupDir(), 0o700); err != nil {
		return "", err
	}
	dir, err := os.MkdirTemp(paths.BackupDir(), "import-"+time.Now().Format("20060102-150405")+"-")
	if err != nil {
		return "", err
	}
	for _, f := range files {
		b, err := fsutil.ReadBounded(f.Path, config.MaxFileBytes)
		if err != nil {
			return "", err
		}
		rel, err := filepath.Rel(paths.Home(), f.Path)
		if err != nil || !filepath.IsLocal(rel) {
			rel = strings.TrimLeft(f.Path[len(filepath.VolumeName(f.Path)):], `/\`)
		}
		if !filepath.IsLocal(rel) {
			return "", fmt.Errorf("%s: not a clean absolute path", f.Path)
		}
		dst := filepath.Join(dir, f.agent, rel)
		if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
			return "", err
		}
		if err := os.WriteFile(dst, b, 0o600); err != nil {
			return "", err
		}
	}
	return dir, nil
}

// importPrompt is the agent's first prompt. It names the task and gives the
// facts; the skill has the steps, which change with the skill's version.
func importPrompt(files []importFile, backup string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Use the ctxtra skill (%s), task \"Import existing instructions\".\n",
		paths.Tilde(filepath.Join(paths.SkillDir(skill.Base), "SKILL.md")))
	b.WriteString("The files:\n")
	for _, f := range files {
		fmt.Fprintf(&b, "- %s\n", f)
	}
	fmt.Fprintf(&b, "ctxtra backed them up to %s.", quoted(paths.Tilde(backup)))
	return b.String()
}

// quoted is the path in Go quotes when it has control or invisible
// characters, such as a newline, which would add lines to the prompt.
func quoted(path string) string {
	if strings.ContainsFunc(path, func(r rune) bool { return unicode.IsControl(r) || unicode.Is(unicode.Cf, r) }) {
		return strconv.Quote(path)
	}
	return path
}

// runAgent runs bin with the terminal, in dir, and waits for it. The agent
// reads Ctrl-C itself; ctxtra ignores it until the agent exits.
func runAgent(bin string, args []string, dir string) error {
	cmd := exec.Command(bin, args...)
	cmd.Dir = dir
	cmd.Env = absEnv(os.Environ())
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt)
	defer signal.Stop(sig)
	if err := cmd.Start(); err != nil {
		return err
	}
	if err := cmd.Wait(); err != nil {
		return fmt.Errorf("exited: %w", err)
	}
	return nil
}

// pathVars are the variables with a path that ctxtra and the agents read
// (a test checks them against testenv.AgentVars). XDG_CONFIG_HOME isn't
// one: a relative value is ignored, by ctxtra too, and stays so.
var pathVars = []string{"CTXTRA_CONFIG", "CTXTRA_LOG", "CLAUDE_CONFIG_DIR", "CODEX_HOME", "COPILOT_HOME",
	"PI_CODING_AGENT_DIR", "OPENCODE_CONFIG_DIR", "HERMES_HOME", "DSH_HOME", "DEVIN_PROJECT_DIR"}

// absEnv is env with the relative paths in pathVars made absolute: the
// agent runs in another folder.
func absEnv(env []string) []string {
	out := slices.Clone(env)
	for i, kv := range out {
		k, v, _ := strings.Cut(kv, "=")
		if !slices.Contains(pathVars, k) || v == "" || filepath.IsAbs(paths.Expand(v)) {
			continue
		}
		if abs, err := filepath.Abs(v); err == nil {
			out[i] = k + "=" + abs
		}
	}
	return out
}
