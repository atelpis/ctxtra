package config

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"github.com/bmatcuk/doublestar/v4"
)

const maxGlobEntries = 10000

// noMatch is a pattern that matches nothing: reported, and the rest of the
// binding still applies.
type noMatch struct{ what string }

func (e noMatch) Error() string { return "glob matches no " + e.what }

// Expansion happens while loading config so sync reviews the same concrete
// paths that sessions, files, show and explain use.
func (c *Config) expand(root, pattern, what string, src *Source) ([]string, error) {
	pattern = filepath.ToSlash(pattern)
	if !doublestar.ValidatePattern(pattern) {
		return nil, fmt.Errorf("invalid glob pattern")
	}
	if src != nil && src.Pinned() && !within(src.Gen(), root) {
		return nil, fmt.Errorf("glob root is outside the pinned source")
	}
	if err := checkParents(pattern); err != nil {
		return nil, err
	}
	// A checked folder is on disk, not in the store: its glob folder must
	// not lead where the store has nothing.
	if src != nil && src.dir != "" {
		if why := LeftOut(src.dir, root); why != "" {
			return nil, fmt.Errorf("the glob folder %s", why)
		}
	}
	tree := &globFS{FS: os.DirFS(root), root: root, cfg: c, pinned: src != nil && src.Pinned(), left: maxGlobEntries}
	if src != nil {
		tree.checked = src.dir
	}
	var matches []string
	err := doublestar.GlobWalk(tree, pattern, func(p string, d fs.DirEntry) error {
		if !within(root, filepath.Join(root, filepath.FromSlash(p))) {
			return fmt.Errorf("glob result escapes its root")
		}
		if what == "skills" && !d.IsDir() || what == "instructions" && !d.Type().IsRegular() {
			return nil
		}
		if what == "skills" {
			info, err := tree.Stat(path.Join(p, "SKILL.md"))
			if os.IsNotExist(err) {
				return nil
			}
			if err != nil {
				return err
			}
			if !info.Mode().IsRegular() {
				return nil
			}
		}
		matches = append(matches, filepath.Join(root, filepath.FromSlash(p)))
		return nil
	}, doublestar.WithNoFollow(), doublestar.WithFailOnIOErrors())
	if err != nil {
		return nil, err
	}
	if len(matches) == 0 {
		return nil, noMatch{what}
	}
	slices.Sort(matches)
	if tree.pinned {
		for _, p := range matches {
			c.pin(*src, p)
		}
	}
	return matches, nil
}

func checkParents(pattern string) error {
	for _, part := range strings.FieldsFunc(pattern, func(r rune) bool { return strings.ContainsRune("/{},", r) }) {
		if part == ".." {
			return fmt.Errorf("glob alternatives cannot traverse parent folders")
		}
	}
	return nil
}

type globFS struct {
	fs.FS
	root   string
	cfg    *Config
	pinned bool
	left   int
	// checked is the folder of a source that LoadSource checks, or "". Its
	// globs don't see what the store would leave out.
	checked string
}

func (g *globFS) Open(name string) (fs.File, error) {
	g.left--
	if g.left < 0 {
		return nil, fmt.Errorf("glob traversal limit exceeded")
	}
	if g.hidden(name) {
		return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrNotExist}
	}
	return g.FS.Open(name)
}

// hidden reports whether name is a path that the store of a checked source
// wouldn't have.
func (g *globFS) hidden(name string) bool {
	return g.checked != "" && fs.ValidPath(name) && LeftOut(g.checked, filepath.Join(g.root, filepath.FromSlash(name))) != ""
}

func (g *globFS) Stat(name string) (fs.FileInfo, error) {
	g.left--
	if g.left < 0 {
		return nil, fmt.Errorf("glob traversal limit exceeded")
	}
	if !fs.ValidPath(name) {
		return nil, &fs.PathError{Op: "stat", Path: name, Err: fs.ErrInvalid}
	}
	if name == "." {
		return os.Stat(g.root)
	}
	if g.hidden(name) {
		return nil, &fs.PathError{Op: "stat", Path: name, Err: fs.ErrNotExist}
	}
	return os.Lstat(filepath.Join(g.root, filepath.FromSlash(name)))
}

// Bound directory enumeration as well as matches: a recursive glob may visit
// many entries without matching any of them.
func (g *globFS) ReadDir(name string) ([]fs.DirEntry, error) {
	if strings.Count(name, "/") > 64 || g.left <= 0 {
		return nil, fmt.Errorf("glob traversal limit exceeded")
	}
	if g.hidden(name) {
		return nil, nil
	}
	if !g.pinned {
		if err := g.cfg.Trust.Check(filepath.Join(g.root, filepath.FromSlash(name), ".ctxtra-glob")); err != nil {
			return nil, err
		}
	}
	f, err := g.FS.Open(name)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	d, ok := f.(fs.ReadDirFile)
	if !ok {
		return nil, fmt.Errorf("not a directory: %s", name)
	}
	entries, err := d.ReadDir(g.left + 1)
	if err != nil && err != io.EOF {
		return nil, err
	}
	g.left -= len(entries) + 1
	if g.left < 0 {
		return nil, fmt.Errorf("glob traversal limit exceeded")
	}
	entries = slices.DeleteFunc(entries, func(e fs.DirEntry) bool { return g.hidden(path.Join(name, e.Name())) })
	slices.SortFunc(entries, func(a, b fs.DirEntry) int { return strings.Compare(a.Name(), b.Name()) })
	return entries, nil
}
