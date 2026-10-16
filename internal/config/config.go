// Package config loads the ctxtra config and the binding files it includes.
//
// The config file (kind Config) declares sources and lists binding files. A
// binding file holds one or more Binding documents; each one binds instruction
// files and skill folders to the sessions its selector matches. Invalid
// entries are left out and reported in Problems: a broken config never adds
// more than intended.
//
// A source is a folder of the user's own files (trusted-path), or a git
// repository pinned to an accepted commit (git), read from ctxtra's store.
// Files in a source are referenced as ctxtra://<source>/<path>. Every file
// read from disk, except the config itself, is checked with package trust
// first.
package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/atelpis/ctxtra/internal/paths"
	"github.com/atelpis/ctxtra/internal/trust"
)

const (
	// APIVersion is the only accepted apiVersion. It is a preview: keys may
	// still change.
	APIVersion  = "ctxtra/v1alpha1"
	KindConfig  = "Config"
	KindBinding = "Binding"

	// MaxFileBytes limits every file a session reads.
	MaxFileBytes = 1 << 20
	// MaxSessionBytes limits what one session reads in total: the config,
	// binding files, instruction files and SKILL.md files.
	MaxSessionBytes = 4 << 20

	// Scheme starts a reference to a file in a source.
	Scheme = "ctxtra://"
)

// Config is the loaded config with all its bindings.
type Config struct {
	File         string
	Sources      []Source
	Trust        *trust.Policy // checks every file read from disk
	BindingFiles []string      // resolved
	// BindingEntries are the bindings entries as written; only Parse sets
	// them.
	BindingEntries []string
	Bindings       []Binding
	MaxChars       int // budget.maxChars, in characters (Unicode code points); 0 means no limit
	Problems       []string
	pinned         map[string]bool // paths resolved into the store
}

// Reader reads the config and binding files. One Reader is shared by
// everything a session reads, so its limits cover all of it.
type Reader interface {
	Read(name string) ([]byte, error)
}

// Binding is one Binding document.
type Binding struct {
	Name     string
	Source   string // the binding file
	Line     int
	Selector Selector
	Files    []string // resolved paths of instruction files
	Skills   []string // resolved paths of skill folders, each with a SKILL.md
}

// ID names the binding in explain output, logs and injected text.
func (b Binding) ID() string { return b.Name + " (" + paths.Tilde(b.Source) + ")" }

// Load reads the config at path and every binding file it includes. A missing
// config is not an error: there is nothing to inject.
func Load(path string, rd Reader) *Config {
	// Every path is made absolute from here on: the trust check and the read
	// must see the same file.
	if abs, err := filepath.Abs(path); err == nil {
		path = abs
	}
	c := &Config{File: path}
	c.Trust = c.policy()
	data, err := rd.Read(path)
	if errors.Is(err, os.ErrNotExist) {
		return c
	}
	if err != nil {
		c.problem(path, err)
		return c
	}
	docs, err := parseDocs(data)
	if err != nil {
		c.problem(path, err)
		return c
	}
	switch {
	case len(docs) == 0:
		return c
	case len(docs) > 1:
		c.problem(path, errors.New("the config must be a single YAML document"))
		return c
	}
	// The whole config is checked before any binding file is read: with a
	// broken budget or source, using the bindings would add more than
	// intended.
	entries, err := c.decode(docs[0])
	if err != nil {
		c.problem(path, fmt.Errorf("%s; nothing from this file is used", flat(err)))
		return c
	}
	c.Trust = c.policy()
	seen := map[string]bool{}
	for _, e := range entries {
		file, err := c.ref(e.Value, filepath.Dir(path), nil)
		if err != nil {
			c.problem(path, fmt.Errorf("line %d: %w", e.Line, err))
			continue
		}
		if seen[file] {
			c.problem(path, fmt.Errorf("line %d: %s is listed twice", e.Line, e.Value))
			continue
		}
		seen[file] = true
		c.BindingFiles = append(c.BindingFiles, file)
		c.loadBindings(file, rd, c.pinnedSource(e.Value))
	}
	return c
}

// decode checks the config document, sets the sources and the budget, and
// returns the bindings entries.
func (c *Config) decode(d document) ([]*yaml.Node, error) {
	if d.err != nil {
		return nil, d.err
	}
	f, err := fields(d.node, "apiVersion", "kind", "sources", "bindings", "budget")
	if err != nil {
		return nil, err
	}
	if err := header(d.node, f, KindConfig); err != nil {
		return nil, err
	}
	if v := f["sources"]; v != nil {
		if err := c.decodeSources(v); err != nil {
			return nil, err
		}
	}
	if v := f["budget"]; v != nil {
		if err := c.decodeBudget(v); err != nil {
			return nil, err
		}
	}
	v := f["bindings"]
	if v == nil {
		return nil, nil
	}
	if _, err := strList(v, "bindings"); err != nil {
		return nil, err
	}
	return v.Content, nil
}

func (c *Config) decodeBudget(n *yaml.Node) error {
	f, err := fields(n, "maxChars")
	if err != nil {
		return err
	}
	v := f["maxChars"]
	if v == nil {
		return nil
	}
	m, err := strconv.Atoi(v.Value)
	if v.ShortTag() != "!!int" || err != nil || m < 0 {
		return fmt.Errorf("line %d: budget.maxChars must be a whole number, 0 or more", v.Line)
	}
	c.MaxChars = m
	return nil
}

// loadBindings adds the Binding documents of one binding file. A broken
// document is skipped; the others in the file still apply. in is the pinned
// source the file is in, or nil.
func (c *Config) loadBindings(file string, rd Reader, in *Source) {
	if in == nil {
		if err := c.Trust.Check(file); err != nil {
			c.problem(file, err)
			return
		}
	}
	data, err := rd.Read(file)
	if err != nil {
		c.problem(file, err)
		return
	}
	docs, err := parseDocs(data)
	if err != nil {
		c.problem(file, fmt.Errorf("%s; nothing from this file is used", flat(err)))
		return
	}
	if len(docs) == 0 {
		c.problem(file, errors.New("no bindings in this file"))
		return
	}
	names := map[string]bool{}
	for _, d := range docs {
		err := d.err
		var b Binding
		if err == nil {
			b, err = c.decodeBinding(d.node, file, in)
		}
		if err == nil && names[b.Name] {
			err = fmt.Errorf("line %d: duplicate name %q", d.node.Line, b.Name)
		}
		if err != nil {
			c.problem(file, fmt.Errorf("%s; binding skipped", flat(err)))
			continue
		}
		names[b.Name] = true
		c.Bindings = append(c.Bindings, b)
	}
}

// ValidName reports whether name can be a binding or source name.
func ValidName(name string) bool { return reName.MatchString(name) }

var reName = regexp.MustCompile(`^[a-zA-Z0-9]([a-zA-Z0-9._-]*[a-zA-Z0-9])?$`)

func (c *Config) decodeBinding(doc *yaml.Node, file string, in *Source) (Binding, error) {
	f, err := fields(doc, "apiVersion", "kind", "metadata", "spec")
	if err != nil {
		return Binding{}, err
	}
	if err := header(doc, f, KindBinding); err != nil {
		return Binding{}, err
	}
	if err := require(doc, f, "metadata", "spec"); err != nil {
		return Binding{}, err
	}
	b := Binding{Source: file, Line: doc.Line}
	meta, err := fields(f["metadata"], "name")
	if err != nil {
		return Binding{}, err
	}
	if err := require(f["metadata"], meta, "name"); err != nil {
		return Binding{}, err
	}
	if b.Name, err = str(meta["name"], "metadata.name"); err != nil {
		return Binding{}, err
	}
	if !reName.MatchString(b.Name) {
		return Binding{}, fmt.Errorf("line %d: name %q may only contain letters, digits, '.', '_' and '-'", meta["name"].Line, b.Name)
	}
	spec, err := fields(f["spec"], "selector", "files", "skills")
	if err != nil {
		return Binding{}, err
	}
	if err := require(f["spec"], spec, "selector"); err != nil {
		return Binding{}, err
	}
	if spec["files"] == nil && spec["skills"] == nil {
		return Binding{}, fmt.Errorf("line %d: spec needs files, skills or both", f["spec"].Line)
	}
	if b.Selector, err = decodeSelector(spec["selector"]); err != nil {
		return Binding{}, err
	}
	if v := spec["files"]; v != nil {
		if b.Files, err = c.pathList(v, "files", file, in); err != nil {
			return Binding{}, err
		}
	}
	if v := spec["skills"]; v != nil {
		if b.Skills, err = c.pathList(v, "skills", file, in); err != nil {
			return Binding{}, err
		}
	}
	return b, nil
}

// header checks apiVersion and kind.
func header(doc *yaml.Node, f map[string]*yaml.Node, kind string) error {
	if err := require(doc, f, "apiVersion", "kind"); err != nil {
		return err
	}
	if v := f["apiVersion"]; v.Value != APIVersion {
		return fmt.Errorf("line %d: apiVersion must be %s", v.Line, APIVersion)
	}
	if v := f["kind"]; v.Value != kind {
		return fmt.Errorf("line %d: kind must be %s", v.Line, kind)
	}
	return nil
}

// pathList resolves the paths listed in file with ref. Globs are not
// supported: every file and skill folder is listed by name. A reference to
// a pinned source that isn't available is skipped alone, with a problem.
func (c *Config) pathList(n *yaml.Node, what, file string, in *Source) ([]string, error) {
	list, err := strList(n, what)
	if err != nil {
		return nil, err
	}
	base := filepath.Dir(file)
	var out []string
	for _, p := range list {
		if strings.ContainsAny(p, "*?[") {
			return nil, fmt.Errorf("line %d: %s entry %q looks like a glob; list each path by name", n.Line, what, p)
		}
		r, err := c.ref(p, base, in)
		var unavailable errUnavailable
		switch {
		case errors.As(err, &unavailable):
			c.problem(file, fmt.Errorf("line %d: %w", n.Line, err))
			continue
		case err != nil:
			return nil, fmt.Errorf("line %d: %w", n.Line, err)
		}
		out = append(out, r)
	}
	return out, nil
}

func (c *Config) problem(file string, err error) {
	c.Problems = append(c.Problems, paths.Tilde(file)+": "+flat(err))
}
