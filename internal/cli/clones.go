package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/atelpis/ctxtra/internal/config"
	"github.com/atelpis/ctxtra/internal/fsutil"
	"github.com/atelpis/ctxtra/internal/paths"
	"github.com/atelpis/ctxtra/internal/source"
	"github.com/atelpis/ctxtra/internal/state"
)

// importClone adds the clone that the binding file p is in as a git source:
// the clone's remote and upstream branch, starting at the clone's committed
// HEAD. Plain paths under the clone are then served from the source (a
// mapping in the local state), also after the clone moves or is deleted.
// Later syncs follow the remote branch, not the clone.
func (s *sources) importClone(p, top, name, remote, branch string) error {
	c, err := source.InspectClone(s.ctx, top)
	if err != nil {
		return err
	}
	names := slices.Sorted(func(yield func(string) bool) {
		for n := range c.Remotes {
			if !yield(n) {
				return
			}
		}
	})
	switch {
	case remote != "":
		if c.Remotes[remote] == "" {
			return fmt.Errorf("%s has no remote %q (remotes: %s)", paths.Tilde(c.Top), remote, strings.Join(names, ", "))
		}
	case len(names) == 1:
		remote = names[0]
	default:
		return fmt.Errorf("%s has several remotes (%s); choose one with --remote", paths.Tilde(c.Top), strings.Join(names, ", "))
	}
	if branch == "" {
		if c.Upstream != remote || c.Branch == "" {
			return fmt.Errorf("the current branch of %s has no upstream on %s; choose the branch to follow with --branch", paths.Tilde(c.Top), remote)
		}
		branch = c.Branch
	}
	url := c.Remotes[remote]
	if err := source.CheckURL(url); err != nil {
		return fmt.Errorf("the remote %s of %s: %w", remote, paths.Tilde(c.Top), err)
	}
	if err := source.CheckBranch(branch); err != nil {
		return err
	}
	rel, ok := paths.Rel(c.Top, paths.Real(p))
	if !ok {
		return fmt.Errorf("%s is not in %s", paths.Tilde(p), paths.Tilde(c.Top))
	}
	cur, err := s.current()
	if err != nil {
		return err
	}
	// A source with the URL is there already: map the clone to it and use
	// its commit; it never moves.
	var same []config.Source
	for _, src := range cur.Sources {
		if src.Git == url {
			same = append(same, src)
		}
	}
	if len(same) > 0 {
		return s.mapToSource(same, c.Top, url, rel)
	}
	if name == "" {
		name = defaultName(url)
	}
	if err := checkName(cur, name, url); err != nil {
		return err
	}
	r := source.Open(url)
	commit, err := r.FetchHead(s.ctx, c.Top)
	if err != nil {
		return err
	}
	gen, err := r.Materialize(s.ctx, commit)
	if err != nil {
		return err
	}
	if st, err := os.Lstat(filepath.Join(gen, filepath.FromSlash(rel))); err != nil || !st.Mode().IsRegular() {
		return fmt.Errorf("%s is not committed in %s; ctxtra uses only committed files", rel, paths.Tilde(c.Top))
	}
	// After the fetch: the mapping and the config change together.
	unlock, err := transition()
	if err != nil {
		return err
	}
	defer unlock()
	added, err := state.Map(c.Top, name, url)
	if err != nil {
		return err
	}
	ref := config.Scheme + name + "/" + rel
	err = s.edit(func(old []byte) ([]byte, error) {
		data, err := config.AddSource(old, name, [][2]string{{"git", url}, {"branch", branch}, {"commit", commit}})
		if err == nil {
			data, err = config.AddBinding(data, ref)
		}
		return data, err
	})
	if err != nil {
		if added {
			state.Unmap(c.Top)
		}
		return err
	}
	fmt.Fprintf(s.Out, "%s is a clone of %s (not trusted), added as source %s\n", paths.Tilde(c.Top), url, name)
	fmt.Fprintf(s.Out, "starts at your local commit %s; later sync follows %s/%s (remote not checked yet)\n", source.Short(commit), remote, branch)
	fmt.Fprintf(s.Out, "added %s (pinned: changes apply after `ctxtra sync`)\n", ref)
	s.showBindings(filepath.Join(gen, filepath.FromSlash(rel)), gen)
	s.showExecutables(r, commit)
	fmt.Fprintf(s.Out, "if the repository is yours, read it from disk instead: ctxtra add --trust %s\n", paths.Tilde(p))
	return nil
}

// mapToSource maps the clone top to the one source in same, which has the
// clone's URL, and adds the file rel from it, at the source's commit.
func (s *sources) mapToSource(same []config.Source, top, url, rel string) error {
	if len(same) > 1 {
		return fmt.Errorf("several sources use %s; reference the file as ctxtra://<source>/%s", url, rel)
	}
	unlock, err := transition()
	if err != nil {
		return err
	}
	defer unlock()
	added, err := state.Map(top, same[0].Name, url)
	if err != nil {
		return err
	}
	fmt.Fprintf(s.Out, "%s is a clone of %s, the source %s\n", paths.Tilde(top), url, same[0].Name)
	if err := s.addRef(config.Scheme + same[0].Name + "/" + rel); err != nil {
		if added {
			state.Unmap(top)
		}
		return err
	}
	return nil
}

// addPinnedPath adds a clone as a pinned-path source: pinned like a git
// source, but sync follows the clone's committed HEAD instead of a remote.
// It adds no mapping: its files are referenced as ctxtra://<name>/<path>.
func (s *sources) addPinnedPath(dir, name string) error {
	c, err := source.InspectClone(s.ctx, dir)
	if err != nil {
		return err
	}
	cur, err := s.current()
	if err != nil {
		return err
	}
	for _, src := range cur.Sources {
		if src.PinnedPath != "" && sameFile(src.PinnedPath, c.Top) {
			return fmt.Errorf("%s is the source %s already", paths.Tilde(c.Top), src.Name)
		}
	}
	if name == "" {
		name = defaultName(c.Top)
	}
	if err := checkName(cur, name, paths.Tilde(c.Top)); err != nil {
		return err
	}
	r := source.Open(c.Top)
	commit, err := r.FetchHead(s.ctx, c.Top)
	if err != nil {
		return err
	}
	gen, err := r.Materialize(s.ctx, commit)
	if err != nil {
		return err
	}
	err = s.edit(func(old []byte) ([]byte, error) {
		return config.AddSource(old, name, [][2]string{{"pinned-path", paths.Tilde(c.Top)}, {"commit", commit}})
	})
	if err != nil {
		return err
	}
	s.checked(config.Source{Name: name, PinnedPath: c.Top})
	fmt.Fprintf(s.Out, "added source %s (pinned-path: %s at %s); sync follows the clone's committed HEAD\n",
		name, paths.Tilde(c.Top), source.Short(commit))
	s.listBindingFiles(name, gen)
	return nil
}

// trustMapped trusts the folder of an imported clone, m. Without a name,
// the clone's source becomes a trusted-path source under its own name, so
// that its ctxtra:// references are read from disk too, and the mapping
// goes. With a name, a new trusted source is added and the pinned one
// stays: plain paths under the folder are then live, ctxtra:// references
// to the pinned source stay pinned. p is the file or folder given; a file is
// added to bindings.
func (s *sources) trustMapped(cur *config.Config, m state.Mapping, p, name string) error {
	unlock, err := transition()
	if err != nil {
		return err
	}
	defer unlock()
	i := slices.IndexFunc(cur.Sources, func(src config.Source) bool { return src.Name == m.Source && src.Git == m.URL })
	if i < 0 || name != "" {
		trusted, err := s.addTrusted(cur, m.Path, name)
		if err != nil {
			return err
		}
		if i >= 0 {
			fmt.Fprintf(s.Out, "note: %s/... paths in your bindings were served from %s and are now live\n", paths.Tilde(m.Path), cur.Sources[i].Name)
		}
		return s.addTrustedFile(trusted, m.Path, p)
	}
	src := cur.Sources[i]
	err = s.edit(func(old []byte) ([]byte, error) {
		// The source as it is now: the one the clone was imported as.
		c, err := config.Parse(old)
		if err != nil {
			return nil, err
		}
		if now, ok := c.Source(src.Name); !ok || now.Git != m.URL {
			return nil, fmt.Errorf("the source %s changed in %s; nothing changed", src.Name, paths.Tilde(s.file))
		}
		return config.ReplaceSource(old, src.Name, [][2]string{{"trusted-path", paths.Tilde(m.Path)}})
	})
	if err != nil {
		return err
	}
	if err := state.Unmap(m.Path); err != nil {
		return err
	}
	fmt.Fprintf(s.Out, "converted source %s to trusted-path %s: its files are read from disk now, changes apply right away\n",
		src.Name, paths.Tilde(m.Path))
	return s.addTrustedFile(src.Name, m.Path, p)
}

// addTrustedFile adds p to bindings through the trusted source name at
// root, when p is a file.
func (s *sources) addTrustedFile(name, root, p string) error {
	if st, err := os.Stat(p); err != nil || st.IsDir() {
		return nil
	}
	rel, ok := paths.Rel(root, paths.Real(p))
	if !ok {
		return nil
	}
	if err := s.addBinding(config.Scheme + name + "/" + rel); err != nil {
		return err
	}
	s.showBindings(paths.Real(p), "")
	return nil
}

// transition serializes the commands that change the config and the
// mappings together (import, trust conversion, remove), so that one can't
// undo what another did.
func transition() (func(), error) {
	return fsutil.Lock(filepath.Join(paths.StateDir(), "sources.lock"))
}

const removeUsage = "usage: ctxtra remove <source>|<bindings entry>\n"

func runRemove(args []string, stdio IO) int {
	if len(args) != 1 || strings.HasPrefix(args[0], "-") {
		fmt.Fprint(stdio.Err, removeUsage)
		return 2
	}
	s := newSources(stdio)
	if err := s.remove(args[0]); err != nil {
		return s.fail("remove", err)
	}
	return 0
}

// remove removes a source with its bindings entries, or one bindings entry.
// A clone mapping of a removed source stays: its old paths are skipped,
// never read from disk.
func (s *sources) remove(what string) error {
	unlock, err := transition()
	if err != nil {
		return err
	}
	defer unlock()
	cur, err := s.current()
	if err != nil {
		return err
	}
	if src, ok := cur.Source(what); ok {
		prefix := config.Scheme + src.Name + "/"
		n := 0
		err := s.edit(func(old []byte) ([]byte, error) {
			// The entries as they are now, under the lock.
			c, err := config.Parse(old)
			if err != nil {
				return nil, err
			}
			data, err := config.RemoveSource(old, src.Name)
			n = 0
			for _, e := range c.BindingEntries {
				if err == nil && strings.HasPrefix(e, prefix) {
					data, err = config.RemoveBinding(data, e)
					n++
				}
			}
			return data, err
		})
		if err != nil {
			return err
		}
		entries := "entries"
		if n == 1 {
			entries = "entry"
		}
		fmt.Fprintf(s.Out, "removed source %s and %d bindings %s\n", src.Name, n, entries)
		s.stillUsed(src, prefix)
		return nil
	}
	entry := ""
	base := filepath.Dir(s.file)
	for _, e := range cur.BindingEntries {
		if e == what || !strings.HasPrefix(e, config.Scheme) && !strings.HasPrefix(what, config.Scheme) &&
			paths.Resolve(base, e) == paths.Resolve(base, paths.Expand(what)) {
			entry = e
			break
		}
	}
	if entry == "" {
		return fmt.Errorf("%s is neither a source nor in bindings", what)
	}
	if err := s.edit(func(old []byte) ([]byte, error) { return config.RemoveBinding(old, entry) }); err != nil {
		return err
	}
	fmt.Fprintf(s.Out, "removed %s from bindings\n", entry)
	return nil
}

// stillUsed lists the lines of the user's binding files that reference the
// removed source src: by prefix, or by a path in a clone mapped to it.
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
	for _, f := range cfg.BindingFiles {
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
