package config

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/atelpis/ctxtra/internal/paths"
	"github.com/atelpis/ctxtra/internal/source"
	"github.com/atelpis/ctxtra/internal/trust"
)

// Source is one entry of sources. It has exactly one kind: TrustedPath or
// Git.
type Source struct {
	Name string
	Line int
	// trusted-path: a folder of the user's own files, read live from disk.
	TrustedPath string // absolute, ~ expanded
	// git: a repository pinned to Commit, read from ctxtra's store. Commit
	// is "" until the first sync.
	Git, Branch, Commit string
}

// Pinned reports whether s is read from the store.
func (s Source) Pinned() bool { return s.Git != "" }

// errUnavailable marks a reference to a pinned source whose accepted commit
// isn't in the store: only that reference is skipped, not the binding.
type errUnavailable struct{ error }

// Gen is the store folder of the accepted commit.
func (s Source) Gen() string { return source.GenDir(s.Git, s.Commit) }

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
	f, err := fields(n, "trusted-path", "git", "branch", "commit")
	if err != nil {
		return Source{}, err
	}
	var s Source
	switch {
	case f["trusted-path"] != nil && f["git"] != nil:
		return Source{}, fmt.Errorf("line %d: a source is either trusted-path or git, not both", n.Line)
	case f["trusted-path"] != nil:
		for _, k := range []string{"branch", "commit"} {
			if v := f[k]; v != nil {
				return Source{}, fmt.Errorf("line %d: %s is only for git sources", v.Line, k)
			}
		}
		v := f["trusted-path"]
		p, err := str(v, "trusted-path")
		if err != nil {
			return Source{}, err
		}
		if !filepath.IsAbs(p) && p != "~" && !strings.HasPrefix(p, "~/") {
			return Source{}, fmt.Errorf("line %d: trusted-path must be absolute or start with ~", v.Line)
		}
		s.TrustedPath = filepath.Clean(paths.Expand(p))
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
		if v := f["commit"]; v != nil {
			s.Commit, _ = str(v, "commit")
			if !source.ValidCommit(s.Commit) {
				return Source{}, fmt.Errorf("line %d: commit must be a full commit ID in quotes: 40 or 64 lower-case hex digits", v.Line)
			}
		}
	default:
		return Source{}, fmt.Errorf("line %d: a source needs trusted-path or git", n.Line)
	}
	return s, nil
}

// pinnedSource is the pinned source a ctxtra:// reference points into, or
// nil.
func (c *Config) pinnedSource(ref string) *Source {
	rest, ok := strings.CutPrefix(ref, Scheme)
	if !ok {
		return nil
	}
	name, _, _ := strings.Cut(rest, "/")
	if s, ok := c.Source(name); ok && s.Pinned() {
		return &s
	}
	return nil
}

// ref resolves a path in the config or a binding file: a ctxtra://
// reference to a file in a source, or a path that is absolute, starts with
// ~, or is relative to base.
//
// in is the pinned source of the binding file, or nil. A pinned source's
// binding file may only reference files in the same source: by a relative
// path, or by ctxtra:// with its own name. Its content is someone else's, so
// it must not reach the user's files.
func (c *Config) ref(s, base string, in *Source) (string, error) {
	rest, isRef := strings.CutPrefix(s, Scheme)
	if !isRef {
		if in == nil {
			return paths.Resolve(base, s), nil
		}
		p := filepath.Join(base, filepath.FromSlash(s))
		if filepath.IsAbs(filepath.FromSlash(s)) || strings.HasPrefix(s, "~") || !within(in.Gen(), p) {
			return "", fmt.Errorf("%s is not a path in the source %s", s, in.Name)
		}
		c.pin(p)
		return p, nil
	}
	name, p, _ := strings.Cut(rest, "/")
	src, ok := c.Source(name)
	switch {
	case !ok:
		return "", fmt.Errorf("%s: unknown source %q", s, name)
	case in != nil && name != in.Name:
		return "", fmt.Errorf("%s: a binding file in the source %s can't reference other sources", s, in.Name)
	}
	p = filepath.Clean(filepath.FromSlash(p))
	if p == "." || filepath.IsAbs(p) || p == ".." || strings.HasPrefix(p, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("%s: needs a path inside the source", s)
	}
	if !src.Pinned() {
		return filepath.Join(src.TrustedPath, p), nil
	}
	switch {
	case src.Commit == "":
		return "", errUnavailable{fmt.Errorf("%s: the source %s has no accepted commit yet; run: ctxtra sync %s; skipped", s, name, name)}
	case !source.Has(src.Git, src.Commit):
		return "", errUnavailable{fmt.Errorf("%s: %s %s is not in ctxtra's store; run: ctxtra sync --restore; skipped", s, name, source.Short(src.Commit))}
	}
	p = filepath.Join(src.Gen(), p)
	c.pin(p)
	return p, nil
}

func (c *Config) pin(p string) {
	if c.pinned == nil {
		c.pinned = map[string]bool{}
	}
	c.pinned[p] = true
}

// within reports whether p is root or below it, compared as cleaned paths.
func within(root, p string) bool {
	rel, err := filepath.Rel(root, filepath.Clean(p))
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}
