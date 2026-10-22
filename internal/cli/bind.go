package cli

import (
	"bytes"
	"errors"
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

// bindingAdd adds a binding to the config. Only a binding that works is
// written: its files exist and are trusted, as a session would read them.
func bindingAdd(stdio IO, b config.NewBinding, dryRun bool) error {
	switch {
	case !config.ValidName(b.Name):
		return fmt.Errorf("name %q may only contain letters, digits, '.', '_' and '-'", b.Name)
	case !b.Global && len(b.Dirs) == 0 && len(b.Repos) == 0:
		return errors.New("say which sessions get it: --global, --dir or --repo")
	case b.Global && (len(b.Dirs) > 0 || len(b.Repos) > 0):
		return errors.New("--global already matches every session; leave out --dir and --repo")
	case len(b.Instructions) == 0 && len(b.Skills) == 0:
		return errors.New("add at least one --instruction or --skill")
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
	b.Dirs, b.Instructions, b.Skills = mapList(b.Dirs, path), mapList(b.Instructions, path), mapList(b.Skills, path)

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
	if dryRun {
		// Reads only: it works where the config can't be written, for
		// example in an agent's sandbox.
		var old []byte
		if old, err = readConfig(s.file); err == nil {
			_, err = change(old)
		}
	} else {
		err = s.edit(change)
	}
	if err != nil {
		return exitCode(s.fail("binding add", err))
	}
	verb := "added"
	if dryRun {
		verb = "would add"
	}
	fmt.Fprintf(s.Out, "%s binding %s to %s: for %s\n", verb, b.Name, paths.Tilde(s.file), added.Selector)
	if len(b.Instructions) > 0 {
		fmt.Fprintf(s.Out, "  instructions: %s\n", strings.Join(b.Instructions, ", "))
	}
	if len(b.Skills) > 0 {
		fmt.Fprintf(s.Out, "  skills: %s\n", strings.Join(b.Skills, ", "))
	}
	if dryRun {
		fmt.Fprintln(s.Out, "nothing changed (--dry-run)")
		return nil
	}
	fmt.Fprintln(s.Out, "new sessions get it; a running session keeps what it got at its start")
	return nil
}

// readConfig reads the config for a dry run: a missing one is empty, a
// broken one an error.
func readConfig(file string) ([]byte, error) {
	old, err := fsutil.ReadBounded(file, config.MaxFileBytes)
	switch {
	case errors.Is(err, os.ErrNotExist):
		return nil, nil
	case err == nil && len(bytes.TrimSpace(old)) > 0:
		if err := config.Check(old); err != nil {
			return nil, fmt.Errorf("%s: %v; fix it first", paths.Tilde(file), err)
		}
	}
	return old, err
}

// bindingInfo is a binding in the output of binding list.
type bindingInfo struct {
	Name         string   `json:"name"`
	File         string   `json:"file"`
	Line         int      `json:"line"`
	Match        string   `json:"match"`
	Instructions []string `json:"instructions"`
	Skills       []string `json:"skills"`
}

// bindingList is binding list: the config's bindings, then those of each
// binding file, as sessions read them.
func bindingList(stdio IO, asJSON bool) int {
	cfg := config.Load(paths.ConfigPath(), newReader())
	out := []bindingInfo{}
	for _, b := range cfg.Bindings {
		out = append(out, bindingInfo{b.Name, b.Source, b.Line, b.Selector.String(),
			append([]string{}, b.Instructions...), append([]string{}, b.Skills...)})
	}
	if asJSON {
		writeJSON(stdio.Out, out, diagnostics("error", cfg.Problems))
	} else {
		if len(out) == 0 {
			fmt.Fprintln(stdio.Out, "no bindings (add one with ctxtra binding add)")
		}
		for _, b := range cfg.Bindings {
			fmt.Fprintf(stdio.Out, "%s: for %s\n", b.ID(), b.Selector)
			for _, f := range b.Instructions {
				fmt.Fprintf(stdio.Out, "  instruction  %s\n", paths.Tilde(f))
			}
			for _, d := range b.Skills {
				fmt.Fprintf(stdio.Out, "  skill        %s\n", paths.Tilde(d))
			}
		}
		for _, p := range cfg.Problems {
			fmt.Fprintf(stdio.Err, "problem: %s\n", p)
		}
	}
	if len(cfg.Problems) > 0 {
		return 1
	}
	return 0
}

// bindingRemove is binding remove: it removes one of the config's own
// bindings, also one that is broken and that sessions skip.
func bindingRemove(stdio IO, name string, dryRun bool) int {
	s := newSources(stdio)
	old, err := readConfig(s.file)
	if err != nil {
		return s.fail("binding remove", err)
	}
	if _, err := config.RemoveBinding(old, name); err != nil {
		for _, b := range config.Load(s.file, newReader()).Bindings {
			if b.Name == name {
				return s.fail("binding remove", fmt.Errorf("%s is in the binding file %s, not in the config; remove that file with ctxtra binding-file remove, or edit it",
					name, paths.Tilde(b.Source)))
			}
		}
		return s.fail("binding remove", fmt.Errorf("no binding named %s in %s", name, paths.Tilde(s.file)))
	}
	if dryRun {
		fmt.Fprintf(s.Out, "would remove binding %s from %s\nnothing changed (--dry-run)\n", name, paths.Tilde(s.file))
		return 0
	}
	if err := s.edit(func(old []byte) ([]byte, error) { return config.RemoveBinding(old, name) }); err != nil {
		return s.fail("binding remove", err)
	}
	fmt.Fprintf(s.Out, "removed binding %s from %s\n", name, paths.Tilde(s.file))
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
