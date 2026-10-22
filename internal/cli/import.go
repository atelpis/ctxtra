package cli

import (
	"bufio"
	"errors"
	"flag"
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

// runImport is ctxtra import: in a terminal, it offers to start an agent
// that moves the instruction files the agents have of their own into ctxtra,
// as setup does for a new config. With --no-start, or without a terminal
// (an agent runs it), it only backs the files up and lists them.
func runImport(args []string, stdio IO) int {
	fs := flag.NewFlagSet("import", flag.ContinueOnError)
	fs.SetOutput(stdio.Err)
	noStart := fs.Bool("no-start", false, "only back up and list the files")
	if err := fs.Parse(args); err != nil {
		return parseError(err)
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(stdio.Err, "ctxtra import: unexpected argument %q\n", fs.Arg(0))
		return 2
	}
	files, launchers, err := importCandidates()
	if err != nil {
		fmt.Fprintf(stdio.Err, "ctxtra import: %v\n", err)
		return 1
	}
	if len(files) == 0 {
		fmt.Fprintln(stdio.Out, "the agents that are set up have no instruction files of their own")
		return 0
	}
	if *noStart || !interactive(stdio.In) {
		fmt.Fprintln(stdio.Out, "instruction files of the agents that are set up:")
		for _, f := range files {
			fmt.Fprintf(stdio.Out, "  %s\n", f)
		}
		if unreadable(stdio, "import", files) {
			return 1
		}
		backup, err := backupFiles(files)
		if err != nil {
			fmt.Fprintf(stdio.Err, "ctxtra import: back up the files: %v\n", err)
			return 1
		}
		fmt.Fprintf(stdio.Out, "backed up the files to %s\n", paths.Tilde(backup))
		return 0
	}
	if skillBound(skill.Base) == nil {
		fmt.Fprintln(stdio.Err, "ctxtra import: the agent follows the ctxtra skill: install it first with ctxtra skill install")
		return 1
	}
	return importFiles(stdio, bufio.NewReader(stdio.In), "import", files, launchers)
}

// offerImport is ctxtra import after setup, for a new config: it asks only
// in a terminal, with the ctxtra skill installed, and while no binding adds
// instructions.
func offerImport(stdio IO, in *bufio.Reader) int {
	if !interactive(stdio.In) || skillBound(skill.Base) == nil || hasInstructions() {
		return 0
	}
	files, launchers, err := importCandidates()
	if err != nil || len(files) == 0 {
		return 0
	}
	fmt.Fprintln(stdio.Out)
	return importFiles(stdio, in, "setup", files, launchers)
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

// importFiles lists the files, asks whether to start an agent for them, or
// which one, backs them up, and starts the agent with a prompt that names
// the files and their backup. cmd names the command in messages.
func importFiles(stdio IO, in *bufio.Reader, cmd string, files []importFile, launchers []agent.Agent) int {
	term.Fprintf(stdio.Out, term.Bold, "Your agents have instruction files of their own:\n")
	for _, f := range files {
		fmt.Fprintf(stdio.Out, "  %s\n", f)
	}
	if unreadable(stdio, cmd, files) {
		return 1
	}
	fmt.Fprint(stdio.Out, `An agent can move them into ctxtra, so that all your agents get them: it merges
the rules that repeat and shows you the result. ctxtra backs the files up
first; the agent empties them only if you agree.
`)
	if len(launchers) == 1 {
		term.Fprintf(stdio.Out, term.Bold, "Start %s for this now? [Y/n] ", launchers[0].Delivery().Title)
	} else {
		term.Fprintf(stdio.Out, term.Bold, "Do this now? [Y/n] ")
	}
	var chosen agent.Agent
	ok, err := confirm(in)
	switch {
	case err != nil || !ok:
	case len(launchers) == 1:
		chosen = launchers[0]
	case len(launchers) > 1:
		chosen, ok = choose(stdio, in, launchers)
	}
	if err != nil {
		fmt.Fprintf(stdio.Err, "ctxtra %s: %v\n", cmd, err)
		return 1
	}
	if !ok {
		term.Fprintf(stdio.Out, term.Dim, "not started; to do it later: ctxtra import\n")
		return 0
	}
	backup, err := backupFiles(files)
	if err != nil {
		fmt.Fprintf(stdio.Err, "ctxtra %s: back up the instruction files: %v\n", cmd, err)
		return 1
	}
	term.Fprintf(stdio.Out, term.Green, "backed up the files to %s\n", paths.Tilde(backup))
	// The prompt names the skill's files: write them, also when they are gone.
	if _, err := skill.Write(skill.Base); err != nil {
		fmt.Fprintf(stdio.Err, "ctxtra %s: write the ctxtra skill: %v\n", cmd, err)
		return 1
	}
	prompt := importPrompt(files, backup)
	if chosen == nil {
		fmt.Fprintf(stdio.Out, "start an agent and give it this prompt:\n\n%s\n", prompt)
		return 0
	}
	a := chosen
	argv := a.(agent.Launcher).Launch(prompt)
	dir := filepath.Join(paths.StateDir(), "import")
	fmt.Fprintf(stdio.Out, "starting %s in %s with this prompt:\n\n%s\n\n", a.Delivery().Title, paths.Tilde(dir), prompt)
	bin, err := lookPath(argv[0])
	if err == nil {
		err = os.MkdirAll(dir, 0o700)
	}
	if err == nil {
		err = startAgent(bin, argv[1:], dir)
	}
	if err != nil {
		fmt.Fprintf(stdio.Err, "ctxtra %s: %s: %v; you can give the prompt above to an agent\n", cmd, a.Delivery().Title, err)
		return 1
	}
	return 0
}

// unreadable reports, and says, whether some of the files can't be read:
// ctxtra can't back them up, and they would be left out.
func unreadable(stdio IO, cmd string, files []importFile) bool {
	if !slices.ContainsFunc(files, func(f importFile) bool { return f.Err != nil }) {
		return false
	}
	fmt.Fprintf(stdio.Err, "ctxtra %s: can't read some of the files above: fix or move them, then run ctxtra import\n", cmd)
	return true
}

// choose asks which of the agents to start; Enter chooses the first. ok is
// false without a valid choice.
func choose(stdio IO, in *bufio.Reader, agents []agent.Agent) (_ agent.Agent, ok bool) {
	term.Fprintf(stdio.Out, term.Bold, "Which agent?\n")
	for i, a := range agents {
		fmt.Fprintf(stdio.Out, "  %d) %s\n", i+1, a.Delivery().Title)
	}
	term.Fprintf(stdio.Out, term.Bold, "Choose 1-%d [1]: ", len(agents))
	answer, err := in.ReadString('\n')
	if err != nil {
		// Input that ends without a line (Ctrl-D) is no answer.
		return nil, false
	}
	n := 1
	if a := strings.TrimSpace(answer); a != "" {
		if n, err = strconv.Atoi(a); err != nil || n < 1 || n > len(agents) {
			return nil, false
		}
	}
	return agents[n-1], true
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
