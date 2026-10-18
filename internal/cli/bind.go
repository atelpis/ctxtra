package cli

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/atelpis/ctxtra/internal/config"
	"github.com/atelpis/ctxtra/internal/fsutil"
	"github.com/atelpis/ctxtra/internal/paths"
	"github.com/atelpis/ctxtra/internal/resolve"
)

const bindUsage = `usage: ctxtra bind NAME (--global | --dir PATTERN... | --repo REMOTE...) [--file FILE]... [--skill FOLDER]... [--dry-run]
Quote patterns, or the shell expands them: --dir '~/dev/work/**'.
`

// runBind adds a binding to the config. Only a binding that works is
// written: its files exist and are trusted, as a session would read them.
func runBind(args []string, stdio IO) int {
	fs := flag.NewFlagSet("bind", flag.ContinueOnError)
	fs.SetOutput(stdio.Err)
	fs.Usage = func() {
		fmt.Fprint(stdio.Err, bindUsage)
		fs.PrintDefaults()
	}
	var b config.NewBinding
	var dirs, repos, files, skills listFlag
	fs.BoolVar(&b.Global, "global", false, "add it to every session")
	fs.Var(&dirs, "dir", "a folder pattern (repeatable); x/** also matches x")
	fs.Var(&repos, "repo", "a remote pattern (repeatable), such as github.com/acme/**")
	fs.Var(&files, "file", "an instruction file (repeatable)")
	fs.Var(&skills, "skill", "a skill folder with a SKILL.md (repeatable)")
	dryRun := fs.Bool("dry-run", false, "check the binding and show it; change nothing")
	// The name may come before or after the flags.
	var pos []string
	for {
		if err := fs.Parse(args); err != nil {
			return parseError(err)
		}
		if fs.NArg() == 0 {
			break
		}
		pos, args = append(pos, fs.Arg(0)), fs.Args()[1:]
	}
	fail := func(msg string) int {
		fmt.Fprintf(stdio.Err, "ctxtra bind: %s\n%s", msg, bindUsage)
		return 2
	}
	switch {
	case len(pos) != 1:
		return fail("expected one binding name")
	case !config.ValidName(pos[0]):
		return fail(fmt.Sprintf("name %q may only contain letters, digits, '.', '_' and '-'", pos[0]))
	case !b.Global && len(dirs) == 0 && len(repos) == 0:
		return fail("say which sessions get it: --global, --dir or --repo")
	case b.Global && (len(dirs) > 0 || len(repos) > 0):
		return fail("--global already matches every session; leave out --dir and --repo")
	case len(files) == 0 && len(skills) == 0:
		return fail("add at least one --file or --skill")
	}
	// Paths are relative to the current folder here, and are written
	// absolute: in the config, they would be relative to its folder.
	wd, _ := os.Getwd()
	path := func(p string) string {
		if strings.HasPrefix(p, config.Scheme) {
			return p
		}
		return paths.Tilde(paths.Resolve(wd, p))
	}
	b.Name, b.Dirs, b.Repos = pos[0], mapList(dirs, path), repos
	b.Instructions, b.Skills = mapList(files, path), mapList(skills, path)

	s := newSources(stdio)
	var added *config.Binding
	change := func(old []byte) ([]byte, error) {
		out, err := config.AddBinding(old, b)
		if err != nil {
			return nil, err
		}
		added, err = checkBinding(s.file, old, out, b.Name, wd)
		return out, err
	}
	var err error
	if *dryRun {
		// Reads only: it works where the config can't be written, for
		// example in an agent's sandbox.
		var old []byte
		old, err = fsutil.ReadBounded(s.file, config.MaxFileBytes)
		switch {
		case errors.Is(err, os.ErrNotExist):
			old, err = nil, nil
		case err == nil && len(bytes.TrimSpace(old)) > 0:
			if err = config.Check(old); err != nil {
				err = fmt.Errorf("%s: %v; fix it first", paths.Tilde(s.file), err)
			}
		}
		if err == nil {
			_, err = change(old)
		}
	} else {
		err = s.edit(change)
	}
	if err != nil {
		return s.fail("bind", err)
	}
	verb := "added"
	if *dryRun {
		verb = "would add"
	}
	fmt.Fprintf(s.Out, "%s binding %s to %s: for %s\n", verb, b.Name, paths.Tilde(s.file), added.Selector)
	if len(b.Instructions) > 0 {
		fmt.Fprintf(s.Out, "  instructions: %s\n", strings.Join(b.Instructions, ", "))
	}
	if len(b.Skills) > 0 {
		fmt.Fprintf(s.Out, "  skills: %s\n", strings.Join(b.Skills, ", "))
	}
	if *dryRun {
		fmt.Fprintln(s.Out, "nothing changed (--dry-run)")
		return 0
	}
	fmt.Fprintln(s.Out, "new sessions get it; a running session keeps what it got at its start")
	return 0
}

// checkBinding loads the config as out, the changed file, and returns the
// binding name in it. The change fails when it brings a new problem, or
// when a session that the binding matches wouldn't get one of its files:
// missing, not trusted, a SKILL.md without a description, or over
// budget.max-chars by itself.
func checkBinding(file string, old, out []byte, name, dir string) (*config.Binding, error) {
	before := config.Load(file, withConfig{file, old, newReader()})
	rd := newReader()
	after := config.Load(file, withConfig{file, out, rd})
	// Problems are counted: the new binding can bring the same problem as
	// one that exists, for example a reference to a commit not in the store.
	// Line numbers are left out: the new binding moves the lines below it,
	// and the changed file isn't written.
	seen := map[string]int{}
	for _, p := range before.Problems {
		seen[reLine.ReplaceAllString(p, "")]++
	}
	var problems []string
	for _, p := range after.Problems {
		p = reLine.ReplaceAllString(p, "")
		if seen[p] > 0 {
			seen[p]--
			continue
		}
		problems = append(problems, p)
	}
	i := slices.IndexFunc(after.Bindings, func(b config.Binding) bool { return b.Source == after.File && b.Name == name })
	if i >= 0 {
		// Resolved alone, as if it matched: what a session it matches
		// would get from it.
		one := *after
		b := after.Bindings[i]
		b.Selector = config.Selector{Global: true}
		one.Bindings, one.Problems = []config.Binding{b}, nil
		res := resolve.Resolve(dir, &one, rd)
		problems = append(problems, res.Problems...)
		for _, m := range res.Missing {
			problems = append(problems, "missing: "+paths.Tilde(m))
		}
		for _, h := range res.Dropped {
			problems = append(problems, fmt.Sprintf("%s alone is over budget.max-chars (%d)", paths.Tilde(h.Path), res.MaxChars))
		}
	}
	if len(problems) > 0 {
		return nil, fmt.Errorf("sessions wouldn't get the binding as asked; nothing changed:\n  %s", strings.Join(problems, "\n  "))
	}
	if i < 0 {
		return nil, fmt.Errorf("the binding %s isn't in the changed config; nothing changed", name)
	}
	return &after.Bindings[i], nil
}

var reLine = regexp.MustCompile(`line \d+: `)

func newReader() *fsutil.Reader { return fsutil.NewReader(config.MaxFileBytes, config.MaxSessionBytes) }

// withConfig reads data as the config file, and other files from disk.
type withConfig struct {
	file string
	data []byte
	rd   config.Reader
}

func (r withConfig) Read(name string) ([]byte, error) {
	if abs, err := filepath.Abs(r.file); err == nil && name == abs {
		return r.data, nil
	}
	return r.rd.Read(name)
}
