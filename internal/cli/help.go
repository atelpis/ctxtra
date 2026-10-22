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
	"github.com/atelpis/ctxtra/internal/term"
)

// topic is a docs page that ctxtra help shows: a file, or one section of it.
type topic struct {
	name, file, section, about string
}

var topics = []topic{
	{"config", "docs/spec.md", "", "the config format and its rules: match, sources, trust, sync, limits"},
	{"scenarios", "docs/scenarios.md", "", "use cases step by step, and what ctxtra protects you from"},
	{"agents", "docs/guide.md", "## Limits", "what differs between agents: size limits, resumed sessions, subagents"},
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

// runSkill shows one of ctxtra's skills, or installs it: the ctxtra skill
// when no name is given.
func runSkill(args []string, stdio IO) int {
	install := len(args) > 0 && args[0] == "install"
	if install {
		args = args[1:]
	}
	if len(args) > 1 {
		fmt.Fprintf(stdio.Err, "usage: ctxtra skill [install] [NAME]\nskills: %s\n", strings.Join(ctxtra.SkillNames(), ", "))
		return 2
	}
	name := skill.Base
	if len(args) == 1 {
		name = args[0]
	}
	if ctxtra.Skill(name) == nil {
		fmt.Fprintf(stdio.Err, "ctxtra skill: no skill %q; skills: %s\n", name, strings.Join(ctxtra.SkillNames(), ", "))
		return 2
	}
	if install {
		if err := installSkill(stdio, name); err != nil {
			fmt.Fprintf(stdio.Err, "ctxtra skill install: %v\n", err)
			return 1
		}
		return 0
	}
	if err := printSkill(stdio.Out, name); err != nil {
		fmt.Fprintf(stdio.Err, "ctxtra skill: %v\n", err)
		return 1
	}
	return 0
}

// printSkill writes SKILL.md of the skill name, then each other file of it
// after a line that names it, so that the output pasted into an agent is
// the whole skill.
func printSkill(w io.Writer, name string) error {
	src := ctxtra.Skill(name)
	b, err := fs.ReadFile(src, "SKILL.md")
	if err != nil {
		return err
	}
	w.Write(b)
	return fs.WalkDir(src, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || p == "SKILL.md" {
			return err
		}
		b, err := fs.ReadFile(src, p)
		if err != nil {
			return err
		}
		fmt.Fprintf(w, "\n<!-- ctxtra skill file: %s -->\n\n", p)
		w.Write(b)
		return nil
	})
}

// installSkill writes the skill name to paths.SkillDir, and adds a binding
// of the same name that adds it to every session, unless a binding has it
// already.
func installSkill(stdio IO, name string) error {
	changed, err := skill.Write(name)
	if err != nil {
		return err
	}
	dir := paths.Tilde(paths.SkillDir(name))
	if changed {
		term.Fprintf(stdio.Out, term.Green, "Installed to %s\n", dir)
	} else {
		fmt.Fprintf(stdio.Out, "The %s skill in %s is up to date.\n", name, dir)
	}
	if b := skillBound(name); b != nil {
		fmt.Fprintf(stdio.Out, "Binding %s adds the skill to sessions.\n", b.ID())
		return nil
	}
	s := newSources(stdio)
	created, err := s.change(func(old []byte) ([]byte, error) {
		return config.AddBinding(old, config.NewBinding{Name: name, Global: true, Skills: []string{dir}})
	})
	if err != nil {
		return err
	}
	if created {
		term.Fprintf(stdio.Out, term.Green, "Created %s\n", paths.Tilde(s.file))
	} else {
		term.Fprintf(stdio.Out, term.Green, "Added to %s\n", paths.Tilde(s.file))
	}
	ready := "The skill is ready for new agent sessions in any folder."
	if name == skill.Base {
		ready += " Ask your agent to help you get started with ctxtra."
	}
	fmt.Fprintln(stdio.Out, ready)
	return nil
}

// skillBound is the first binding of the config that adds the installed
// skill name, or nil.
func skillBound(name string) *config.Binding {
	cfg := config.Load(paths.ConfigPath(), fsutil.NewReader(config.MaxFileBytes, config.MaxSessionBytes))
	dir := paths.Real(paths.SkillDir(name))
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
	return ok && term.IsTerminal(f)
}

// offers are the skills that setup offers, in order: a hint for a session
// without a terminal, and the title and text of the question.
var offers = []struct{ name, hint, title, about string }{
	{skill.Base, "to let your agents help you set up and change ctxtra: ctxtra skill install", "Your agents can set up ctxtra for you",
		"ctxtra comes with a skill that makes getting started easy. Your agents can create your bindings, change your config, and bring the instructions you already have into ctxtra."},
	{follow, "to let your agents get your bindings for other folders they work in: ctxtra skill install " + follow, "Work across multiple projects in one session?",
		"The " + follow + " skill helps your agent pick up the instructions and skills you've configured for each project as your work moves between them."},
}

// follow is the skill that gets the bindings of a folder an agent's work
// moves to.
const follow = "ctxtra-follow"

// offerSkills asks, after setup, whether to install each skill of offers
// that no binding adds; Enter says yes. Without a terminal, for example
// when an agent runs setup, it says how instead.
func offerSkills(stdio IO, in *bufio.Reader) error {
	for _, o := range offers {
		if skillBound(o.name) != nil {
			continue
		}
		if !interactive(stdio.In) {
			fmt.Fprintln(stdio.Out, o.hint)
			continue
		}
		fmt.Fprintln(stdio.Out)
		term.Fprintf(stdio.Out, term.Bold, "%s\n", o.title)
		fmt.Fprintf(stdio.Out, "%s\n\n", o.about)
		term.Fprintf(stdio.Out, term.Bold, "Install the %s skill? [Y/n] ", o.name)
		ok, err := confirm(in)
		if err != nil {
			return err
		}
		if !ok {
			later := "ctxtra skill install"
			if o.name != skill.Base {
				later += " " + o.name
			}
			term.Fprintf(stdio.Out, term.Dim, "Skipped. To install it later: %s\n", later)
			continue
		}
		fmt.Fprintln(stdio.Out)
		if err := installSkill(stdio, o.name); err != nil {
			return err
		}
	}
	return nil
}

func confirm(in *bufio.Reader) (bool, error) {
	answer, err := in.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return false, err
	}
	a := strings.ToLower(strings.TrimSpace(answer))
	return err == nil && (a == "" || a == "y" || a == "yes"), nil
}
