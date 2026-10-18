package cli

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"slices"
	"strings"

	"github.com/atelpis/ctxtra"
	"github.com/atelpis/ctxtra/internal/config"
	"github.com/atelpis/ctxtra/internal/fsutil"
	"github.com/atelpis/ctxtra/internal/paths"
	"github.com/atelpis/ctxtra/internal/skill"
)

// topic is a docs page that ctxtra help shows: a file, or one section of it.
type topic struct {
	name, file, section, about string
}

var topics = []topic{
	{"config", "docs/spec.md", "", "the config format and its rules: match, sources, trust, sync, limits"},
	{"scenarios", "docs/scenarios.md", "", "use cases step by step, and what ctxtra protects you from"},
	{"agents", "README.md", "## Limits", "what differs between agents: size limits, resumed sessions, subagents"},
}

// helpTopics lists the topics, for the usage text.
func helpTopics() string {
	var b strings.Builder
	for _, t := range topics {
		fmt.Fprintf(&b, "  %-10s %s\n", t.name, t.about)
	}
	return b.String()
}

// runHelp shows the usage text, or a docs page of this version.
func runHelp(args []string, stdio IO) int {
	if len(args) == 0 {
		fmt.Fprint(stdio.Out, usage)
		return 0
	}
	if len(args) == 1 {
		for _, t := range topics {
			if t.name == args[0] {
				text, err := t.read()
				if err != nil {
					fmt.Fprintf(stdio.Err, "ctxtra help: %v\n", err)
					return 1
				}
				fmt.Fprint(stdio.Out, text)
				return 0
			}
		}
	}
	fmt.Fprintf(stdio.Err, "ctxtra help: no topic %q. Topics:\n%s", strings.Join(args, " "), helpTopics())
	return 2
}

// read returns the topic's text: the file, or its section up to the next
// heading of the same level.
func (t topic) read() (string, error) {
	b, err := ctxtra.Doc(t.file)
	if err != nil {
		return "", err
	}
	text := string(b)
	if t.section == "" {
		return text, nil
	}
	_, rest, ok := strings.Cut(text, "\n"+t.section+"\n")
	if !ok {
		return "", fmt.Errorf("%s has no section %q", t.file, t.section)
	}
	level := t.section[:strings.Index(t.section, " ")+1]
	if i := strings.Index(rest, "\n"+level); i >= 0 {
		rest = rest[:i+1]
	}
	return t.section + "\n" + rest, nil
}

// skillBinding is the name of the binding that skill install adds.
const skillBinding = "ctxtra"

// runSkill shows the ctxtra skill, or installs it.
func runSkill(args []string, stdio IO) int {
	switch {
	case len(args) == 0:
		if err := printSkill(stdio.Out); err != nil {
			fmt.Fprintf(stdio.Err, "ctxtra skill: %v\n", err)
			return 1
		}
		return 0
	case len(args) == 1 && args[0] == "install":
		if err := installSkill(stdio); err != nil {
			fmt.Fprintf(stdio.Err, "ctxtra skill install: %v\n", err)
			return 1
		}
		return 0
	}
	fmt.Fprintln(stdio.Err, "usage: ctxtra skill [install]")
	return 2
}

// printSkill writes SKILL.md, then each other file of the skill after a
// line that names it, so that the output pasted into an agent is the whole
// skill.
func printSkill(w io.Writer) error {
	b, err := fs.ReadFile(ctxtra.Skill(), "SKILL.md")
	if err != nil {
		return err
	}
	w.Write(b)
	return fs.WalkDir(ctxtra.Skill(), ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || p == "SKILL.md" {
			return err
		}
		b, err := fs.ReadFile(ctxtra.Skill(), p)
		if err != nil {
			return err
		}
		fmt.Fprintf(w, "\n<!-- ctxtra skill file: %s -->\n\n", p)
		w.Write(b)
		return nil
	})
}

// installSkill writes the skill to paths.SkillDir, and adds a binding that
// adds it to every session, unless a binding has it already.
func installSkill(stdio IO) error {
	changed, err := skill.Write()
	if err != nil {
		return err
	}
	dir := paths.Tilde(paths.SkillDir())
	if changed {
		fmt.Fprintf(stdio.Out, "wrote the ctxtra skill to %s\n", dir)
	} else {
		fmt.Fprintf(stdio.Out, "the ctxtra skill in %s is up to date\n", dir)
	}
	s := newSources(stdio)
	if b := skillBound(); b != nil {
		fmt.Fprintf(stdio.Out, "binding %s adds it to sessions\n", b.ID())
		return nil
	}
	err = s.edit(func(old []byte) ([]byte, error) {
		return config.AddBinding(old, config.NewBinding{Name: skillBinding, Global: true, Skills: []string{dir}})
	})
	if err != nil {
		return err
	}
	fmt.Fprintf(stdio.Out, "added binding %s to %s: every new session gets the skill\n", skillBinding, paths.Tilde(s.file))
	return nil
}

// skillBound is the first binding of the config that adds the installed
// skill, or nil.
func skillBound() *config.Binding {
	cfg := config.Load(paths.ConfigPath(), fsutil.NewReader(config.MaxFileBytes, config.MaxSessionBytes))
	dir := paths.Real(paths.SkillDir())
	for i, b := range cfg.Bindings {
		if slices.ContainsFunc(b.Skills, func(s string) bool { return paths.Real(s) == dir }) {
			return &cfg.Bindings[i]
		}
	}
	return nil
}

// interactive reports whether in is a terminal; a variable so tests can
// replace it.
var interactive = func(in io.Reader) bool {
	f, ok := in.(*os.File)
	if !ok {
		return false
	}
	st, err := f.Stat()
	return err == nil && st.Mode()&os.ModeCharDevice != 0
}

// offerSkill asks, after setup, whether to install the skill; Enter says
// yes. Without a terminal, for example when an agent runs setup, it says
// how instead.
func offerSkill(stdio IO) error {
	if skillBound() != nil {
		return nil
	}
	if !interactive(stdio.In) {
		fmt.Fprintln(stdio.Out, "to let your agents help you set up and change ctxtra: ctxtra skill install")
		return nil
	}
	fmt.Fprint(stdio.Out, `
ctxtra comes with a skill for your agents:
it helps you get started, manage your config, and fix problems.
You can install it later with: ctxtra skill install
Install it now? [Y/n] `)
	answer, err := bufio.NewReader(stdio.In).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	// Input that ends without a line (Ctrl-D) is no answer.
	a := strings.ToLower(strings.TrimSpace(answer))
	if err != nil || a != "" && a != "y" && a != "yes" {
		fmt.Fprintln(stdio.Out, "not installed")
		return nil
	}
	return installSkill(stdio)
}
