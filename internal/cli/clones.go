package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/atelpis/ctxtra/internal/config"
	"github.com/atelpis/ctxtra/internal/fsutil"
	"github.com/atelpis/ctxtra/internal/paths"
	"github.com/atelpis/ctxtra/internal/source"
	"github.com/atelpis/ctxtra/internal/state"
)

// transition serializes the commands that change the config and the
// mappings together (adding a clone, trust, remove), so that one can't undo
// what another did.
func transition() (func(), error) {
	return fsutil.Lock(filepath.Join(paths.StateDir(), "sources.lock"))
}

// clones are the folders known as clones of the pinned source src: a
// local-git source's clone, or the clones mapped to a git source.
func clones(src config.Source) []string {
	if src.LocalGit != "" {
		return []string{src.LocalGit}
	}
	var out []string
	ms, _ := state.LoadMappings()
	for _, m := range ms {
		if m.Source == src.Name && m.URL == src.Git {
			out = append(out, m.Path)
		}
	}
	return out
}

// trust converts the pinned source name to a trusted-path source that reads
// dir, in place: references to it keep working and read the folder from
// disk. The mapping of dir goes; other clones mapped to the source keep
// theirs, and their paths stay skipped, never read from disk.
func (s *sources) trust(name, dir string, dryRun bool) error {
	cur, err := s.current()
	if err != nil {
		return err
	}
	src, ok := cur.Source(name)
	switch {
	case !ok:
		return fmt.Errorf("no source named %s", name)
	case !src.Pinned():
		return fmt.Errorf("%s is a trusted-path source already", name)
	}
	known := clones(src)
	if dir == "" {
		switch len(known) {
		case 0:
			return fmt.Errorf("no clone of %s is known; name the folder of yours: ctxtra source trust %s DIR", name, name)
		case 1:
			dir = known[0]
		default:
			return fmt.Errorf("%s has several clones (%s); name the one to read: ctxtra source trust %s DIR",
				name, strings.Join(mapList(known, paths.Tilde), ", "), name)
		}
	}
	if st, err := os.Stat(dir); err != nil {
		return err
	} else if !st.IsDir() {
		return fmt.Errorf("%s is not a folder", paths.Tilde(dir))
	}
	if src.Git != "" && !slices.ContainsFunc(known, func(k string) bool { return sameFile(k, dir) }) {
		if c, err := source.InspectClone(s.ctx, dir); err != nil || !slices.Contains(mapValues(c.Remotes), src.Git) {
			fmt.Fprintf(s.Err, "warning: %s is not a clone of %s\n", paths.Tilde(dir), src.Git)
		}
	}
	if dryRun {
		fmt.Fprintf(s.Out, "would convert source %s to trusted-path %s: its files would be read from disk, changes would apply right away\n",
			name, paths.Tilde(dir))
		fmt.Fprintln(s.Out, "nothing changed (--dry-run)")
		return nil
	}
	unlock, err := transition()
	if err != nil {
		return err
	}
	defer unlock()
	err = s.edit(func(old []byte) ([]byte, error) {
		c, err := config.Parse(old)
		if err != nil {
			return nil, err
		}
		if now, ok := c.Source(name); !ok || now.Remote() != src.Remote() {
			return nil, fmt.Errorf("the source %s changed in %s; nothing changed", name, paths.Tilde(s.file))
		}
		return config.ReplaceSource(old, name, [][2]string{{"trusted-path", paths.Tilde(dir)}})
	})
	if err != nil {
		return err
	}
	if src.Git != "" {
		if err := state.Unmap(paths.Real(dir)); err != nil {
			return err
		}
	}
	fmt.Fprintf(s.Out, "converted source %s to trusted-path %s: its files are read from disk now, changes apply right away\n",
		name, paths.Tilde(dir))
	return nil
}

func mapValues(m map[string]string) []string {
	var out []string
	for _, v := range m {
		out = append(out, v)
	}
	return out
}

// removeSource removes a source with its include entries. A clone mapping
// of a removed source stays: its old paths are skipped, never read from
// disk.
func (s *sources) removeSource(name string, dryRun bool) error {
	if !dryRun {
		// A dry run writes nothing, also no lock.
		unlock, err := transition()
		if err != nil {
			return err
		}
		defer unlock()
	}
	cur, err := s.current()
	if err != nil {
		return err
	}
	src, ok := cur.Source(name)
	if !ok {
		return fmt.Errorf("no source named %s", name)
	}
	prefix := config.Scheme + src.Name + "/"
	var entries []string
	if dryRun {
		for _, e := range cur.Includes {
			if strings.HasPrefix(e, prefix) {
				entries = append(entries, e)
			}
		}
	} else {
		err = s.edit(func(old []byte) ([]byte, error) {
			// The entries as they are now, under the lock.
			c, err := config.Parse(old)
			if err != nil {
				return nil, err
			}
			data, err := config.RemoveSource(old, src.Name)
			entries = nil
			for _, e := range c.Includes {
				if err == nil && strings.HasPrefix(e, prefix) {
					data, err = config.RemoveInclude(data, e)
					entries = append(entries, e)
				}
			}
			return data, err
		})
		if err != nil {
			return err
		}
	}
	verb := "removed"
	if dryRun {
		verb = "would remove"
	}
	fmt.Fprintf(s.Out, "%s source %s\n", verb, src.Name)
	for _, e := range entries {
		fmt.Fprintf(s.Out, "%s binding file %s\n", verb, e)
	}
	s.stillUsed(src, prefix)
	if dryRun {
		fmt.Fprintln(s.Out, "nothing changed (--dry-run)")
	}
	return nil
}

// removeBindingFile removes one entry from include: as written, or a path
// to the same file.
func (s *sources) removeBindingFile(what string, dryRun bool) error {
	cur, err := s.current()
	if err != nil {
		return err
	}
	entry := ""
	base := filepath.Dir(s.file)
	wd, _ := os.Getwd()
	for _, e := range cur.Includes {
		if e == what {
			entry = e
			break
		}
		if strings.HasPrefix(e, config.Scheme) || strings.HasPrefix(what, config.Scheme) {
			continue
		}
		if a, b := paths.Resolve(base, e), paths.Resolve(wd, what); a == b || sameFile(a, b) {
			entry = e
			break
		}
	}
	if entry == "" {
		return fmt.Errorf("%s is not in the include list (see ctxtra binding-file list)", what)
	}
	if dryRun {
		fmt.Fprintf(s.Out, "would remove binding file %s\nnothing changed (--dry-run)\n", entry)
		return nil
	}
	if err := s.edit(func(old []byte) ([]byte, error) { return config.RemoveInclude(old, entry) }); err != nil {
		return err
	}
	fmt.Fprintf(s.Out, "removed binding file %s\n", entry)
	return nil
}

// stillUsed lists the lines of the config and the user's binding files that
// reference the removed source src: by prefix, or by a path in a clone
// mapped to it.
func (s *sources) stillUsed(src config.Source, prefix string) {
	needles := []string{prefix}
	ms, _ := state.LoadMappings()
	for _, m := range ms {
		if m.Source == src.Name && m.URL == src.Git {
			needles = append(needles, m.Path+"/", paths.Tilde(m.Path)+"/")
		}
	}
	cfg := config.Load(s.file, fsutil.NewReader(config.MaxFileBytes, config.MaxSessionBytes))
	var found []string
	for _, f := range append([]string{cfg.File}, cfg.BindingFiles...) {
		if _, inStore := paths.Rel(source.StoreDir(), f); inStore {
			continue
		}
		b, err := fsutil.ReadBounded(f, config.MaxFileBytes)
		if err != nil {
			continue
		}
		for i, line := range strings.Split(string(b), "\n") {
			if slices.ContainsFunc(needles, func(n string) bool { return strings.Contains(line, n) }) {
				found = append(found, fmt.Sprintf("  %s:%d  %s", paths.Tilde(f), i+1, strings.TrimSpace(line)))
			}
		}
	}
	if len(found) > 0 {
		fmt.Fprintln(s.Out, "your bindings still use it:")
		for _, f := range found {
			fmt.Fprintln(s.Out, f)
		}
	}
}

// sourceInfo is a source in the output of source list and show.
type sourceInfo struct {
	Name      string `json:"name"`
	Kind      string `json:"kind"`
	Location  string `json:"location"`
	Branch    string `json:"branch,omitempty"`
	Commit    string `json:"commit,omitempty"`
	InStore   *bool  `json:"in_store,omitempty"`
	LastCheck string `json:"last_check,omitempty"`
	Dismissed string `json:"dismissed,omitempty"`
	// Only in source show.
	BindingFiles []string `json:"binding_files,omitempty"`
	Clones       []string `json:"clones,omitempty"`
}

func info(src config.Source) sourceInfo {
	i := sourceInfo{Name: src.Name, Kind: kind(src), Location: src.Remote(), Branch: src.Branch, Commit: src.Commit}
	if !src.Pinned() {
		i.Location = src.TrustedPath
		return i
	}
	if src.Commit != "" {
		in := source.Has(src.Remote(), src.Commit)
		i.InStore = &in
	}
	e := state.Load().Get(source.ID(src.Remote()), src.Remote(), src.Branch)
	if !e.LastCheck.IsZero() {
		i.LastCheck = e.LastCheck.Format(time.RFC3339)
	}
	if e.Dismissed != "" && e.Dismissed != src.Commit {
		i.Dismissed = e.Dismissed
	}
	return i
}

// list is source list.
func (s *sources) list(asJSON bool) int {
	cur, err := s.current()
	if err != nil {
		return s.fail("source list", err)
	}
	if asJSON {
		out := []sourceInfo{}
		for _, src := range cur.Sources {
			out = append(out, info(src))
		}
		writeJSON(s.Out, out, nil)
		return 0
	}
	if len(cur.Sources) == 0 {
		fmt.Fprintln(s.Out, "no sources (add one with ctxtra source add)")
	}
	for _, src := range cur.Sources {
		fmt.Fprintf(s.Out, "%s: %s\n", src.Name, describeSource(src))
	}
	return 0
}

// show is source show.
func (s *sources) show(name string, asJSON bool) int {
	cur, err := s.current()
	if err != nil {
		return s.fail("source show", err)
	}
	src, ok := cur.Source(name)
	if !ok {
		return s.fail("source show", errors.New("no source named "+name))
	}
	i := info(src)
	for _, e := range cur.Includes {
		if strings.HasPrefix(e, config.Scheme+name+"/") {
			i.BindingFiles = append(i.BindingFiles, e)
		}
	}
	if src.Git != "" {
		i.Clones = clones(src)
	}
	if asJSON {
		writeJSON(s.Out, i, nil)
		return 0
	}
	fmt.Fprintf(s.Out, "source %s: %s\n", name, describeSource(src))
	for _, f := range i.BindingFiles {
		fmt.Fprintf(s.Out, "  binding file: %s\n", f)
	}
	for _, c := range i.Clones {
		fmt.Fprintf(s.Out, "  clone: %s (its paths are read from the source's commit)\n", paths.Tilde(c))
	}
	return 0
}

// bindingFileInfo is an include entry in the output of binding-file list.
type bindingFileInfo struct {
	Entry    string   `json:"entry"`
	Path     string   `json:"path,omitempty"`
	Bindings []string `json:"bindings"`
}

// listBindingFiles is binding-file list: each include entry, the file it
// names, and the bindings it has.
func (s *sources) listBindingFiles(asJSON bool) int {
	cur, err := s.current()
	if err != nil {
		return s.fail("binding-file list", err)
	}
	cfg := config.Load(s.file, fsutil.NewReader(config.MaxFileBytes, config.MaxSessionBytes))
	base := filepath.Dir(s.file)
	out := []bindingFileInfo{}
	for _, e := range cur.Includes {
		i := bindingFileInfo{Entry: e, Bindings: []string{}}
		if name, file, ok := strings.Cut(strings.TrimPrefix(e, config.Scheme), "/"); strings.HasPrefix(e, config.Scheme) && ok {
			if src, found := cur.Source(name); found {
				root := src.TrustedPath
				if src.Pinned() {
					root = src.Gen()
				}
				i.Path = filepath.Join(root, filepath.FromSlash(file))
			}
		} else {
			i.Path = paths.Resolve(base, e)
		}
		for _, b := range cfg.Bindings {
			if i.Path != "" && b.Source == i.Path {
				i.Bindings = append(i.Bindings, b.Name)
			}
		}
		out = append(out, i)
	}
	n := 0
	if len(cfg.Problems) > 0 {
		n = 1
	}
	if asJSON {
		writeJSON(s.Out, out, diagnostics("error", cfg.Problems))
		return n
	}
	if len(out) == 0 {
		fmt.Fprintln(s.Out, "no binding files (include one with ctxtra binding-file add)")
	}
	for _, i := range out {
		bindings := "no bindings"
		if len(i.Bindings) > 0 {
			bindings = "bindings: " + strings.Join(i.Bindings, ", ")
		}
		fmt.Fprintf(s.Out, "%s (%s)\n", i.Entry, bindings)
	}
	for _, p := range cfg.Problems {
		fmt.Fprintf(s.Err, "problem: %s\n", p)
	}
	return n
}
