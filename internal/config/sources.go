package config

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"

	"github.com/atelpis/ctxtra/internal/paths"
	"github.com/atelpis/ctxtra/internal/source"
	"github.com/atelpis/ctxtra/internal/state"
	"github.com/atelpis/ctxtra/internal/trust"
)

// Source is one entry of sources. It has exactly one kind: TrustedPath,
// Git or LocalGit.
type Source struct {
	Name string
	Line int
	// trusted-path: a folder of the user's own files, read live from disk.
	TrustedPath string // absolute, ~ expanded
	// git: a repository pinned to Commit, read from ctxtra's store. Commit
	// is "" until the first sync.
	Git, Branch string
	// local-git: a clone on this disk, pinned to Commit like git; sync
	// follows the clone's committed HEAD.
	LocalGit string // absolute, ~ expanded
	Commit   string
}

// Pinned reports whether s is read from the store.
func (s Source) Pinned() bool { return s.Remote() != "" }

// Remote is where a pinned source's commits come from: the git URL, or the
// clone of a local-git source. "" for a trusted source.
func (s Source) Remote() string {
	if s.Git != "" {
		return s.Git
	}
	return s.LocalGit
}

// errUnavailable marks a reference to a pinned source whose accepted commit
// isn't in the store: only that reference is skipped, not the binding.
type errUnavailable struct{ error }

// Gen is the store folder of the accepted commit.
func (s Source) Gen() string { return source.GenDir(s.Remote(), s.Commit) }

// Source returns the source called name.
func (c *Config) Source(name string) (Source, bool) {
	i := slices.IndexFunc(c.Sources, func(s Source) bool { return s.Name == name })
	if i < 0 {
		return Source{}, false
	}
	return c.Sources[i], true
}

// policy checks files on disk: trusted-path sources are trusted. ctxtra's
// store is denied: its files are read only through ctxtra:// references,
// which keep a pinned source's binding files to their own source. By a plain
// path, a binding file there could reference any file.
func (c *Config) policy() *trust.Policy {
	return trust.New(c.roots(), trust.Root{Name: "ctxtra's store (use ctxtra://<source>/<path>)", Path: source.StoreDir()})
}

// Pinned reports whether path was resolved from a ctxtra:// reference into
// the store, or is a skill's SKILL.md in such a folder. These paths aren't
// checked with Trust: ctxtra wrote the store, without symlinks.
func (c *Config) Pinned(path string) bool {
	return c.pinned[path] || filepath.Base(path) == "SKILL.md" && c.pinned[filepath.Dir(path)]
}

func (c *Config) roots() []trust.Root {
	var roots []trust.Root
	for _, s := range c.Sources {
		if s.TrustedPath != "" {
			roots = append(roots, trust.Root{Name: s.Name, Path: s.TrustedPath})
		}
	}
	return roots
}

// decodeSources reads the sources map.
func (c *Config) decodeSources(n *yaml.Node) error {
	if n.Kind != yaml.MappingNode || len(n.Content) == 0 {
		return fmt.Errorf("line %d: sources must be a non-empty map of names to sources", n.Line)
	}
	for i := 0; i+1 < len(n.Content); i += 2 {
		k, v := n.Content[i], n.Content[i+1]
		name, err := str(k, "source name")
		if err != nil {
			return err
		}
		if !reName.MatchString(name) {
			return fmt.Errorf("line %d: source name %q may only contain letters, digits, '.', '_' and '-'", k.Line, name)
		}
		if _, dup := c.Source(name); dup {
			return fmt.Errorf("line %d: duplicate source %q", k.Line, name)
		}
		s, err := decodeSource(v)
		if err != nil {
			return err
		}
		s.Name, s.Line = name, k.Line
		c.Sources = append(c.Sources, s)
	}
	return nil
}

func decodeSource(n *yaml.Node) (Source, error) {
	f, err := fields(n, "trusted-path", "git", "local-git", "branch", "commit")
	if err != nil {
		return Source{}, err
	}
	kinds := 0
	for _, k := range []string{"trusted-path", "git", "local-git"} {
		if f[k] != nil {
			kinds++
		}
	}
	var s Source
	switch {
	case kinds > 1:
		return Source{}, fmt.Errorf("line %d: a source has one kind: trusted-path, git or local-git", n.Line)
	case f["branch"] != nil && f["git"] == nil:
		return Source{}, fmt.Errorf("line %d: branch is only for git sources", f["branch"].Line)
	case f["commit"] != nil && f["trusted-path"] != nil:
		return Source{}, fmt.Errorf("line %d: commit is only for git and local-git sources", f["commit"].Line)
	case f["trusted-path"] != nil:
		if s.TrustedPath, err = folder(f["trusted-path"], "trusted-path"); err != nil {
			return Source{}, err
		}
		return s, nil
	case f["local-git"] != nil:
		if s.LocalGit, err = folder(f["local-git"], "local-git"); err != nil {
			return Source{}, err
		}
	case f["git"] != nil:
		if err := require(n, f, "branch"); err != nil {
			return Source{}, err
		}
		if s.Git, err = str(f["git"], "git"); err != nil {
			return Source{}, err
		}
		if err := source.CheckURL(s.Git); err != nil {
			return Source{}, fmt.Errorf("line %d: %w", f["git"].Line, err)
		}
		if s.Branch, err = str(f["branch"], "branch"); err != nil {
			return Source{}, err
		}
		if err := source.CheckBranch(s.Branch); err != nil {
			return Source{}, fmt.Errorf("line %d: %w", f["branch"].Line, err)
		}
	default:
		return Source{}, fmt.Errorf("line %d: a source needs trusted-path, git or local-git", n.Line)
	}
	if v := f["commit"]; v != nil {
		s.Commit, _ = str(v, "commit")
		if !source.ValidCommit(s.Commit) {
			return Source{}, fmt.Errorf("line %d: commit must be a full commit ID in quotes: 40 or 64 lower-case hex digits", v.Line)
		}
	}
	return s, nil
}

// folder reads a folder path that is absolute or starts with ~.
func folder(v *yaml.Node, what string) (string, error) {
	p, err := str(v, what)
	if err != nil {
		return "", err
	}
	if !filepath.IsAbs(p) && p != "~" && !strings.HasPrefix(p, "~/") {
		return "", fmt.Errorf("line %d: %s must be absolute or start with ~", v.Line, what)
	}
	return filepath.Clean(paths.Expand(p)), nil
}

// ref resolves a path in the config or a binding file: a ctxtra://
// reference to a file in a source, or a path that is absolute, starts with
// ~, or is relative to base.
//
// in is the pinned source of the binding file, or nil. A pinned source's
// binding file may only reference files in the same source: by a relative
// path, or by ctxtra:// with its own name. Its content is someone else's, so
// it must not reach the user's files.
//
// ref also returns the pinned source the path is in, or nil.
func (c *Config) ref(s, base string, in *Source) (string, *Source, error) {
	return c.refLinks(s, base, in, true)
}

// refLinks is ref; without follow, a plain path is served from an imported
// clone only when it is under the clone's folder as written, not through a
// symlink.
func (c *Config) refLinks(s, base string, in *Source, follow bool) (string, *Source, error) {
	rest, isRef := strings.CutPrefix(s, Scheme)
	if !isRef {
		if in == nil {
			return c.mapped(s, paths.Resolve(base, s), follow)
		}
		p := filepath.Join(base, filepath.FromSlash(s))
		if filepath.IsAbs(filepath.FromSlash(s)) || strings.HasPrefix(s, "~") || !within(in.Gen(), p) {
			return "", nil, fmt.Errorf("%s is not a path in the source %s", s, in.Name)
		}
		c.pin(p)
		return p, in, nil
	}
	name, p, _ := strings.Cut(rest, "/")
	src, ok := c.Source(name)
	switch {
	case !ok:
		return "", nil, fmt.Errorf("%s: unknown source %q", s, name)
	case in != nil && name != in.Name:
		return "", nil, fmt.Errorf("%s: a binding file in the source %s can't reference other sources", s, in.Name)
	}
	p = filepath.Clean(filepath.FromSlash(p))
	if p == "." || filepath.IsAbs(p) || p == ".." || strings.HasPrefix(p, ".."+string(filepath.Separator)) {
		return "", nil, fmt.Errorf("%s: needs a path inside the source", s)
	}
	if !src.Pinned() {
		return filepath.Join(src.TrustedPath, p), nil, nil
	}
	return c.inStore(s, src, p)
}

// inStore is the path p in the accepted commit of the pinned source src.
// s is the reference as written, for messages.
func (c *Config) inStore(s string, src Source, p string) (string, *Source, error) {
	if src.Commit != "" && !c.touched[src.Gen()] && source.Has(src.Remote(), src.Commit) {
		// Before anything is read from it: the store keeps what sessions
		// use. All touches of one load share one wait budget; after a
		// failed one, every generation not touched yet is skipped at once.
		err := c.storeBusy
		if err == nil {
			if !c.waitSet {
				c.waitLeft, c.waitSet = source.TouchWait, true
			}
			start := time.Now()
			err = touch(src.Gen(), max(c.waitLeft, 0))
			c.waitLeft -= time.Since(start)
			c.storeBusy = err
		}
		if err != nil {
			return "", nil, errUnavailable{fmt.Errorf("%s: ctxtra's store is being cleaned (%v); skipped, try again", s, err)}
		}
		if c.touched == nil {
			c.touched = map[string]bool{}
		}
		c.touched[src.Gen()] = true
	}
	switch {
	case src.Commit == "":
		return "", nil, errUnavailable{fmt.Errorf("%s: the source %s has no accepted commit yet; run: ctxtra source update %s; skipped", s, src.Name, src.Name)}
	case !source.Has(src.Remote(), src.Commit):
		return "", nil, errUnavailable{fmt.Errorf("%s: %s %s is not in ctxtra's store; run: ctxtra source restore %s; skipped", s, src.Name, source.Short(src.Commit), src.Name)}
	}
	p = filepath.Join(src.Gen(), filepath.FromSlash(p))
	c.pin(p)
	return p, &src, nil
}

// mapped serves a plain path p from the source a clone was imported as,
// when p is under the clone's folder. A trusted-path source that covers p
// comes first. The source, by name and URL, must still be in the config:
// the path is not read from disk instead.
//
// When mappings.json can't be read, ctxtra can't tell which paths were
// mapped: every plain path outside the trusted-path sources is skipped
// until the file is fixed or deleted.
func (c *Config) mapped(s, p string, follow bool) (string, *Source, error) {
	if c.mappings == nil {
		ms, err := state.LoadMappings()
		c.mappings, c.mappingsErr = &ms, err
	}
	if len(*c.mappings) == 0 && c.mappingsErr == nil {
		return p, nil, nil
	}
	real := paths.Real(p)
	for _, src := range c.Sources {
		if src.TrustedPath == "" {
			continue
		}
		root := paths.Real(src.TrustedPath)
		if within(src.TrustedPath, p) || within(root, real) {
			return p, nil, nil
		}
	}
	if c.mappingsErr != nil {
		return "", nil, errUnavailable{fmt.Errorf("%s: %v; until then, paths outside trusted-path sources are skipped", s, c.mappingsErr)}
	}
	m, rel, ok := c.mappings.Lookup(p)
	if !ok && follow {
		m, rel, ok = c.mappings.Lookup(real)
	}
	if !ok {
		return p, nil, nil
	}
	i := slices.IndexFunc(c.Sources, func(src Source) bool { return src.Name == m.Source && src.Git == m.URL })
	if i < 0 {
		return "", nil, errUnavailable{fmt.Errorf("%s: %s was imported as the source %s (%s), which is not in the config any more; skipped, not read from disk",
			s, paths.Tilde(m.Path), m.Source, m.URL)}
	}
	src := c.Sources[i]
	if rel != "." {
		c.Notes = append(c.Notes, fmt.Sprintf("%s is served from %s; you can write it as %s%s/%s", s, src.Name, Scheme, src.Name, rel))
	}
	return c.inStore(s, src, rel)
}

// touch is source.Touch; a variable for tests.
var touch = source.Touch

func (c *Config) pin(p string) {
	if c.pinned == nil {
		c.pinned = map[string]bool{}
	}
	c.pinned[p] = true
}

// within reports whether p is root or below it, compared as cleaned paths.
func within(root, p string) bool {
	_, ok := paths.Rel(root, p)
	return ok
}
