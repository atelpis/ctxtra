package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/atelpis/ctxtra/internal/testenv"
)

// tree is the command tree, for tests that walk it.
func tree() *cobra.Command {
	var o bytes.Buffer
	root := (&app{IO: IO{In: strings.NewReader(""), Out: &o, Err: &o}, version: "test"}).root()
	root.InitDefaultHelpCmd()
	root.InitDefaultCompletionCmd()
	return root
}

// commands are the visible commands of the tree, below root.
func commands(c *cobra.Command) []*cobra.Command {
	var out []*cobra.Command
	for _, s := range c.Commands() {
		if s.Hidden || s.Name() == "completion" {
			continue
		}
		out = append(out, s)
		out = append(out, commands(s)...)
	}
	return out
}

// Every command has a summary and works with --help; every command that
// runs has an example; an unknown flag is a usage error.
func TestCommandTree(t *testing.T) {
	testenv.Home(t)
	for _, c := range commands(tree()) {
		path := strings.Fields(c.CommandPath())[1:]
		if c.Short == "" {
			t.Errorf("%s: no summary", c.CommandPath())
		}
		if c.Example == "" {
			t.Errorf("%s: no example", c.CommandPath())
		}
		if code, out, _ := run(append(path, "--help")...); code != 0 || !strings.Contains(out, "Usage:") {
			t.Errorf("%s --help: %d %q", c.CommandPath(), code, out)
		}
		if code, _, errOut := run(append(path, "--no-such-flag")...); code != 2 || !strings.Contains(errOut, "unknown flag") {
			t.Errorf("%s --no-such-flag: %d %q", c.CommandPath(), code, errOut)
		}
	}
}

// A help topic can't share a name with a command: help would show only
// one of them.
func TestTopicNames(t *testing.T) {
	root := tree()
	for _, tp := range topics {
		if c, rest, err := root.Find([]string{tp.name}); err == nil && c != root && len(rest) == 0 {
			t.Errorf("topic %s is a command too", tp.name)
		}
	}
}

var (
	reFence = regexp.MustCompile("(?s)```([a-z]*)\n(.*?)```")
	reSpan  = regexp.MustCompile("`(ctxtra [^`]+)`")
)

// Every ctxtra command in the docs and the skills names a command of the
// tree, with flags it has: agents run the skills' commands as written.
func TestDocCommands(t *testing.T) {
	root := tree()
	files := []string{"../../README.md", "../../docs/spec.md", "../../docs/scenarios.md"}
	skills, _ := filepath.Glob("../../skills/*/*.md")
	refs, _ := filepath.Glob("../../skills/*/*/*.md")
	files = append(append(files, skills...), refs...)
	checked := 0
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		text := string(b)
		// Commands in code blocks are command lines; inline ones can name a
		// command without its arguments.
		var cmds []string
		lines := map[string]bool{}
		for _, m := range reFence.FindAllStringSubmatch(text, -1) {
			for _, line := range strings.Split(m[2], "\n") {
				line = strings.TrimSpace(line)
				// Output examples: only the lines with a prompt are commands.
				if m[1] == "text" && !strings.HasPrefix(line, "$ ") {
					continue
				}
				if line = strings.TrimPrefix(line, "$ "); strings.HasPrefix(line, "ctxtra ") {
					cmds = append(cmds, line)
					lines[line] = true
				}
			}
		}
		for _, m := range reSpan.FindAllStringSubmatch(reFence.ReplaceAllString(text, ""), -1) {
			cmds = append(cmds, m[1])
		}
		for _, cmd := range cmds {
			if err := checkDocCommand(root, cmd, lines[cmd]); err != "" {
				t.Errorf("%s: %q: %s", filepath.Base(f), cmd, err)
			}
			checked++
		}
	}
	if checked < 50 {
		t.Errorf("checked only %d commands", checked)
	}
	for _, bad := range []string{"ctxtra version extra", "ctxtra skill install", "ctxtra sync", "ctxtra source update --nope",
		"ctxtra context show --part x"} {
		if checkDocCommand(root, bad, true) == "" {
			t.Errorf("%q passes", bad)
		}
	}
}

// checkDocCommand checks the command path and the flag names of cmd, a
// command line or a usage line of the docs, and, for a command line without
// placeholders, its flag values and arguments.
func checkDocCommand(root *cobra.Command, cmd string, line bool) string {
	var args []string
	for _, tok := range strings.Fields(cmd)[1:] {
		if slices.Contains([]string{"|", "||", "&&", ";", ">", "2>&1", "#"}, tok) {
			break
		}
		args = append(args, strings.Trim(tok, "[](){}.,:"))
	}
	// An uppercase word is a placeholder, such as COMMAND.
	if len(args) > 0 && args[0] == strings.ToUpper(args[0]) && !strings.HasPrefix(args[0], "-") {
		return ""
	}
	c, rest, err := root.Find(args)
	if err != nil {
		return err.Error()
	}
	if c == root && len(rest) > 0 && !strings.HasPrefix(rest[0], "-") {
		return "no command " + rest[0]
	}
	if c.HasSubCommands() && len(rest) > 0 && regexp.MustCompile(`^[a-z][a-z-]*$`).MatchString(rest[0]) {
		return "no command " + rest[0] + " under " + c.CommandPath()
	}
	if c.Name() == "hook" {
		return ""
	}
	for _, a := range args {
		name, _, _ := strings.Cut(strings.TrimLeft(a, "-"), "=")
		if !strings.HasPrefix(a, "-") || slices.Contains([]string{"h", "help", ""}, name) {
			continue
		}
		if c.Flags().Lookup(name) == nil && c.InheritedFlags().Lookup(name) == nil {
			return "no flag " + a + " on " + c.CommandPath()
		}
	}
	if !line || !concrete(cmd) {
		return ""
	}
	// A concrete command line parses: its flag values and its arguments.
	c, rest, _ = tree().Find(args)
	c.InitDefaultHelpFlag()
	if err := c.ParseFlags(rest); err != nil {
		return err.Error()
	}
	if err := c.ValidateArgs(c.Flags().Args()); err != nil && c.Runnable() {
		return err.Error()
	}
	return ""
}

var rePlaceholder = regexp.MustCompile(`(^|[\s=/])[A-Z][A-Z_]+\b|[\[\](){}<>|]|\.\.\.`)

// concrete reports whether cmd is a command line, not a usage line with
// placeholders.
func concrete(cmd string) bool {
	cmd, _, _ = strings.Cut(cmd, " #")
	return !rePlaceholder.MatchString(cmd)
}
