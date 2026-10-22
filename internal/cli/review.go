package cli

import (
	"fmt"
	"reflect"
	"slices"
	"strings"

	"github.com/atelpis/ctxtra/internal/config"
	"github.com/atelpis/ctxtra/internal/fsutil"
	"github.com/atelpis/ctxtra/internal/paths"
	"github.com/atelpis/ctxtra/internal/source"
)

// used is what the config takes from one generation of a source: files and
// skill folders, relative to the generation, slash-separated.
type used struct {
	files map[string]bool
	dirs  []string
}

func (u *used) has(p string) bool {
	return u.files[p] || slices.ContainsFunc(u.dirs, func(d string) bool { return d == "." || strings.HasPrefix(p, d+"/") })
}

// add adds what cfg takes from gen: binding files there, and every file and
// skill folder of every binding, under any match block.
func (u *used) add(cfg *config.Config, gen string) {
	for _, b := range cfg.Bindings {
		for _, f := range append([]string{b.Source}, b.Instructions...) {
			if r, ok := paths.Rel(gen, f); ok {
				u.files[r] = true
			}
		}
		// A skill folder can be the source's top: then every file is used.
		for _, d := range b.Skills {
			if r, ok := paths.Rel(gen, d); ok {
				u.dirs = append(u.dirs, r)
			}
		}
	}
	for _, f := range cfg.BindingFiles {
		if r, ok := paths.Rel(gen, f); ok {
			u.files[r] = true
		}
	}
}

// review shows what changes in src from its accepted commit, or from
// nothing for a source without one, to commit: files the config uses (with
// the accepted commit or the new one) in full, every other changed file by
// name, and warnings for changed match blocks and broken references. With
// full, every changed file in full. Both commits must be in the store.
func (s *sources) review(r *source.Repo, src config.Source, commit string, full bool) error {
	old := src.Commit
	if old == "" {
		old = emptyTree(commit)
	}
	changes, err := r.Changes(s.ctx, old, commit)
	if err != nil {
		return err
	}
	oldGen, newGen := src.Gen(), source.GenDir(src.Remote(), commit)
	before := config.LoadAt(s.file, fsutil.NewReader(config.MaxFileBytes, config.MaxSessionBytes), src.Name, src.Commit)
	after := config.LoadAt(s.file, fsutil.NewReader(config.MaxFileBytes, config.MaxSessionBytes), src.Name, commit)
	u := &used{files: map[string]bool{}}
	u.add(before, oldGen)
	u.add(after, newGen)

	s.selectorWarnings(src, before, after, oldGen, newGen)
	// Problems introduced by the proposed source version.
	for _, p := range after.Problems {
		if (strings.Contains(p, paths.Tilde(newGen)) || strings.Contains(p, config.Scheme+src.Name+"/")) && !slices.Contains(before.Problems, strings.ReplaceAll(p, paths.Tilde(newGen), paths.Tilde(oldGen))) {
			fmt.Fprintf(s.Out, "  error: %s\n", source.Sanitize(strings.ReplaceAll(p, paths.Tilde(newGen)+"/", config.Scheme+src.Name+"/")))
		}
	}

	var shown, other []source.Change
	for _, c := range changes {
		if full || u.has(c.Path) {
			shown = append(shown, c)
		} else {
			other = append(other, c)
		}
	}
	if len(shown) == 0 {
		fmt.Fprintln(s.Out, "no changes in files you use")
	}
	// In batches, so that a large skill doesn't run git once per file.
	for batch := range slices.Chunk(shown, 100) {
		var ps []string
		for _, c := range batch {
			ps = append(ps, c.Path)
		}
		d, err := r.Diff(s.ctx, old, commit, ps...)
		if err != nil {
			return err
		}
		fmt.Fprint(s.Out, d)
		if d != "" && !strings.HasSuffix(d, "\n") {
			fmt.Fprintln(s.Out)
		}
	}
	if len(other) > 0 {
		fmt.Fprintln(s.Out, "also changed, not used by you:")
		for _, c := range other {
			fmt.Fprintf(s.Out, "  %s\n", describeChange(c))
		}
		fmt.Fprintln(s.Out, "Accepting accepts these files too: a file you reference later is used without review. ctxtra source update --full shows them.")
	}
	return nil
}

// emptyTree is git's empty tree in the object format of commit: the base of
// the review of a source's first commit. Git knows it without the object.
func emptyTree(commit string) string {
	if len(commit) == 64 {
		return "6ef19b41225c5369f1c104d45d8d85efa9b057b53b14b4b9b939dd74decc5321"
	}
	return "4b825dc642cb6eb9a060e54bf8d69288fbee4904"
}

// describeChange is one line about a changed file that isn't shown in full.
func describeChange(c source.Change) string {
	mode := func(m string) string {
		switch m {
		case "100755":
			return "executable"
		case "120000":
			return "symlink (left out)"
		case "160000":
			return "submodule (left out)"
		}
		return ""
	}
	note := ""
	switch c.Status {
	case 'A':
		note = mode(c.NewMode)
		return strings.TrimSpace(fmt.Sprintf("added    %s  %s", c.Path, note))
	case 'D':
		return "removed  " + c.Path
	}
	if c.OldMode != c.NewMode {
		note = fmt.Sprintf("mode %s -> %s", strings.TrimPrefix(c.OldMode, "100"), strings.TrimPrefix(c.NewMode, "100"))
		if m := mode(c.NewMode); m != "" {
			note += ", now " + m
		}
	}
	if c.SameContent {
		return "mode     " + c.Path + "  " + note
	}
	return strings.TrimSpace("changed  " + c.Path + "  " + note)
}

// selectorWarnings warns about bindings in the source's binding files whose
// match block changes, and about new ones: they decide which sessions get
// the source's files.
func (s *sources) selectorWarnings(src config.Source, before, after *config.Config, oldGen, newGen string) {
	type key struct{ file, name string }
	selectors := func(cfg *config.Config, gen string) map[key]config.Selector {
		m := map[key]config.Selector{}
		for _, b := range cfg.Bindings {
			if r, ok := paths.Rel(gen, b.Source); ok {
				m[key{r, b.Name}] = b.Selector
			}
		}
		return m
	}
	old, now := selectors(before, oldGen), selectors(after, newGen)
	var keys []key
	for k := range now {
		keys = append(keys, k)
	}
	slices.SortFunc(keys, func(a, b key) int { return strings.Compare(a.file+"\x00"+a.name, b.file+"\x00"+b.name) })
	for _, k := range keys {
		sel := now[k]
		prev, ok := old[k]
		switch {
		case !ok:
			fmt.Fprintf(s.Out, "warning: new binding %s in %s, for %s\n", k.name, k.file, sel)
		case reflect.DeepEqual(prev, sel):
		case sel.Global:
			fmt.Fprintf(s.Out, "warning: binding %s in %s now applies to every session (was: %s)\n", k.name, k.file, prev)
		default:
			fmt.Fprintf(s.Out, "warning: binding %s in %s: match changed from %s to %s\n", k.name, k.file, prev, sel)
		}
	}
}
