// Package config loads the ctxtra config and the binding files it includes.
//
// The config file (kind Config) lists binding files. A binding file holds one
// or more Binding documents; each one binds instruction files and skill folders
// to the sessions its selector matches. Invalid entries are left out and
// reported in Problems: a broken config never adds more than intended.
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
)

// Config is the loaded config with all its bindings.
type Config struct {
	File     string
	Includes []string // binding files, resolved
	Bindings []Binding
	MaxChars int // budget.maxChars, in characters (Unicode code points); 0 means no limit
	Problems []string
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
	c := &Config{File: path}
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
	// broken budget, using the bindings would add more than intended.
	includes, err := c.decode(docs[0], path)
	if err != nil {
		c.problem(path, fmt.Errorf("%s; nothing from this file is used", flat(err)))
		return c
	}
	seen := map[string]bool{}
	for _, inc := range includes {
		file := paths.Resolve(filepath.Dir(path), inc.Value)
		if seen[file] {
			c.problem(path, fmt.Errorf("line %d: %s is included twice", inc.Line, inc.Value))
			continue
		}
		seen[file] = true
		c.Includes = append(c.Includes, file)
		c.loadBindings(file, rd)
	}
	return c
}

// decode checks the config document, sets the budget and returns the include
// entries.
func (c *Config) decode(d document, path string) ([]*yaml.Node, error) {
	if d.err != nil {
		return nil, d.err
	}
	f, err := fields(d.node, "apiVersion", "kind", "include", "budget")
	if err != nil {
		return nil, err
	}
	if err := header(d.node, f, KindConfig); err != nil {
		return nil, err
	}
	if v := f["budget"]; v != nil {
		if err := c.decodeBudget(v); err != nil {
			return nil, err
		}
	}
	v := f["include"]
	if v == nil {
		return nil, nil
	}
	if _, err := strList(v, "include"); err != nil {
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
// document is skipped; the others in the file still apply.
func (c *Config) loadBindings(file string, rd Reader) {
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
			b, err = decodeBinding(d.node, file)
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

var reName = regexp.MustCompile(`^[a-zA-Z0-9]([a-zA-Z0-9._-]*[a-zA-Z0-9])?$`)

func decodeBinding(doc *yaml.Node, file string) (Binding, error) {
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
	base := filepath.Dir(file)
	if v := spec["files"]; v != nil {
		if b.Files, err = pathList(v, "files", base); err != nil {
			return Binding{}, err
		}
	}
	if v := spec["skills"]; v != nil {
		if b.Skills, err = pathList(v, "skills", base); err != nil {
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

// pathList resolves listed paths against base. Globs are not supported: every
// file and skill folder is listed by name.
func pathList(n *yaml.Node, what, base string) ([]string, error) {
	list, err := strList(n, what)
	if err != nil {
		return nil, err
	}
	out := make([]string, len(list))
	for i, p := range list {
		if strings.ContainsAny(p, "*?[") {
			return nil, fmt.Errorf("line %d: %s entry %q looks like a glob; list each path by name", n.Line, what, p)
		}
		out[i] = paths.Resolve(base, p)
	}
	return out, nil
}

func (c *Config) problem(file string, err error) {
	c.Problems = append(c.Problems, paths.Tilde(file)+": "+flat(err))
}
