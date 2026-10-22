package cli

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
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
	{"config", "docs/spec.md", "", "the config format and its rules: match, sources, trust, updates, limits"},
	{"scenarios", "docs/scenarios.md", "", "use cases step by step, and what ctxtra protects you from"},
	{"agents", "README.md", "## Limits", "what differs between agents: size limits, resumed sessions, subagents"},
}

func findTopic(name string) (topic, bool) {
	i := slices.IndexFunc(topics, func(t topic) bool { return t.name == name })
	if i < 0 {
		return topic{}, false
	}
	return topics[i], true
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

// checkSkill fails unless name is one of ctxtra's skills.
func checkSkill(stdio IO, cmd, name string) bool {
	if ctxtra.Skill(name) == nil {
		fmt.Fprintf(stdio.Err, "ctxtra %s: no skill %q; skills: %s\n", cmd, name, skillNames())
		return false
	}
	return true
}

// skillInfo is a skill in the output of skill list.
type skillInfo struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Installed   bool   `json:"installed"`
	Binding     string `json:"binding,omitempty"` // the binding that adds it to sessions
}

// skillList is skill list.
func skillList(stdio IO, asJSON bool) int {
	out := []skillInfo{}
	for _, name := range ctxtra.SkillNames() {
		i := skillInfo{Name: name, Description: description(name)}
		_, err := os.Stat(filepath.Join(paths.SkillDir(name), "SKILL.md"))
		i.Installed = err == nil
		if b := skillBound(name); b != nil {
			i.Binding = b.ID()
		}
		out = append(out, i)
	}
	if asJSON {
		writeJSON(stdio.Out, out, nil)
		return 0
	}
	for _, i := range out {
		state := "not installed"
		switch {
		case i.Binding != "":
			state = "installed; binding " + i.Binding + " adds it to sessions"
		case i.Installed:
			state = "installed, but no binding adds it to sessions"
		}
		fmt.Fprintf(stdio.Out, "%s: %s\n  %s\n", i.Name, state, i.Description)
	}
	return 0
}

// description is the description in the front matter of a skill's
// SKILL.md.
func description(name string) string {
	b, err := fs.ReadFile(ctxtra.Skill(name), "SKILL.md")
	if err != nil {
		return ""
	}
	sc := bufio.NewScanner(bytes.NewReader(b))
	for sc.Scan() {
		if d, ok := strings.CutPrefix(sc.Text(), "description:"); ok {
			return strings.Trim(strings.TrimSpace(d), `"'`)
		}
	}
	return ""
}

// skillShow is skill show: SKILL.md of the skill, then each other file of
// it after a line that names it, so that the output pasted into an agent is
// the whole skill.
func skillShow(stdio IO, name string) int {
	if !checkSkill(stdio, "skill show", name) {
		return 1
	}
	if err := printSkill(stdio.Out, name); err != nil {
		fmt.Fprintf(stdio.Err, "ctxtra skill show: %v\n", err)
		return 1
	}
	return 0
}

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

// skillInstall is skill install: it writes the skill name to
// paths.SkillDir, and adds a binding of the same name that adds it to every
// session, unless a binding has it already.
func skillInstall(stdio IO, name string, dryRun bool) int {
	if !checkSkill(stdio, "skill install", name) {
		return 1
	}
	dir := paths.Tilde(paths.SkillDir(name))
	b := skillBound(name)
	// The binding is checked before anything is written: a skill whose
	// binding can't be added isn't installed.
	newBinding := config.NewBinding{Name: name, Global: true, Skills: []string{dir}}
	if b == nil {
		old, err := readConfig(paths.ConfigPath())
		if err == nil {
			_, err = config.AddBinding(old, newBinding)
		}
		if err != nil {
			fmt.Fprintf(stdio.Err, "ctxtra skill install: %v\n", err)
			return 1
		}
	}
	if dryRun {
		fmt.Fprintf(stdio.Out, "would write the %s skill to %s\n", name, dir)
		if b == nil {
			fmt.Fprintf(stdio.Out, "would add the binding %s, which adds it to every session\n", name)
		}
		fmt.Fprintln(stdio.Out, "nothing changed (--dry-run)")
		return 0
	}
	changed, err := skill.Write(name)
	if err != nil {
		fmt.Fprintf(stdio.Err, "ctxtra skill install: %v\n", err)
		return 1
	}
	if changed {
		term.Fprintf(stdio.Out, term.Green, "Installed to %s\n", dir)
	} else {
		fmt.Fprintf(stdio.Out, "The %s skill in %s is up to date.\n", name, dir)
	}
	if b != nil {
		fmt.Fprintf(stdio.Out, "Binding %s adds the skill to sessions.\n", b.ID())
		return 0
	}
	s := newSources(stdio)
	created, err := s.change(func(old []byte) ([]byte, error) {
		return config.AddBinding(old, newBinding)
	})
	if err != nil {
		fmt.Fprintf(stdio.Err, "ctxtra skill install: %v\n", err)
		return 1
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
	return 0
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
