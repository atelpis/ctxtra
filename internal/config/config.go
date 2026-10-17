// Package config loads the ctxtra config and the binding files it includes.
//
// The config declares sources, has bindings of its own and includes binding
// files. A binding file has only bindings; each one binds instruction files
// and skill folders to the sessions its match block matches. Invalid entries
// are left out and reported in Problems: a broken config never adds more than
// intended.
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
	"slices"
	"strconv"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"

	"github.com/atelpis/ctxtra/internal/paths"
	"github.com/atelpis/ctxtra/internal/state"
	"github.com/atelpis/ctxtra/internal/trust"
)

const (
	// Schema is the only accepted schema, in the config and in binding
	// files.
	Schema = "ctxtra/v1"

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
	BindingFiles []string      // the include entries, resolved
	// Includes are the include entries as written; only Parse sets them.
	Includes    []string
	Bindings    []Binding // the config's own, then those of each binding file
	MaxChars    int       // budget.max-chars, in characters (Unicode code points); 0 means no limit
	Problems    []string
	Notes       []string        // hints, for doctor
	pinned      map[string]bool // paths resolved into the store
	mappings    *state.Mappings // of imported clones; read when needed
	mappingsErr error
	touched     map[string]bool // generations touched for this load
	storeBusy   error           // a touch failed: don't wait again
	waitLeft    time.Duration   // what touches may still wait for the store
	waitSet     bool
}

// Reader reads the config and binding files. One Reader is shared by
// everything a session reads, so its limits cover all of it.
type Reader interface {
	Read(name string) ([]byte, error)
}

// Binding is one entry of bindings, in the config or in a binding file.
type Binding struct {
	Name         string
	Source       string // the file it is in: the config or a binding file
	Line         int
	Selector     Selector // match
	Instructions []string // resolved paths of instruction files
	Skills       []string // resolved paths of skill folders, each with a SKILL.md
}

// ID names the binding in explain output, logs and injected text.
func (b Binding) ID() string { return b.Name + " (" + paths.Tilde(b.Source) + ")" }

// Load reads the config at path and every binding file it includes. A missing
// config is not an error: there is nothing to inject.
func Load(path string, rd Reader) *Config { return loadConfig(path, rd, "", "") }

// LoadAt is Load as if the pinned source name were at commit, for sync to
// see what the config would use from a new commit.
func LoadAt(path string, rd Reader, name, commit string) *Config {
	return loadConfig(path, rd, name, commit)
}

func loadConfig(path string, rd Reader, name, commit string) *Config {
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
	// The whole config is checked before any binding is used: with a broken
	// budget or source, using the bindings would add more than intended.
	includes, own, err := c.decode(docs[0])
	if err != nil {
		c.problem(path, fmt.Errorf("%s; nothing from this file is used", flat(err)))
		return c
	}
	for i := range c.Sources {
		if name != "" && c.Sources[i].Name == name && c.Sources[i].Pinned() {
			c.Sources[i].Commit = commit
		}
	}
	c.Trust = c.policy()
	if own != nil {
		c.addBindings(path, own, nil)
	}
	seen := map[string]bool{}
	for _, e := range includes {
		file, src, err := c.ref(e.Value, filepath.Dir(path), nil)
		if err != nil {
			c.problem(path, fmt.Errorf("line %d: %w", e.Line, err))
			continue
		}
		if file == path || paths.Real(file) == paths.Real(path) {
			c.problem(path, fmt.Errorf("line %d: %s is the config itself; its bindings apply already", e.Line, e.Value))
			continue
		}
		if seen[file] {
			c.problem(path, fmt.Errorf("line %d: %s is listed twice", e.Line, e.Value))
			continue
		}
		seen[file] = true
		c.BindingFiles = append(c.BindingFiles, file)
		c.loadBindings(file, rd, src)
	}
	return c
}

// decode checks the config document and sets the sources and the budget.
// It returns the include entries and the config's own bindings list (nil
// without one). The bindings themselves are checked one by one later: a
// broken binding is skipped alone.
func (c *Config) decode(d document) ([]*yaml.Node, *yaml.Node, error) {
	if d.err != nil {
		return nil, nil, d.err
	}
	f, err := fields(d.node, "schema", "sources", "include", "bindings", "budget")
	if err != nil {
		return nil, nil, err
	}
	if err := checkSchema(d.node, f); err != nil {
		return nil, nil, err
	}
	if v := f["sources"]; v != nil {
		if err := c.decodeSources(v); err != nil {
			return nil, nil, err
		}
	}
	if v := f["budget"]; v != nil {
		if err := c.decodeBudget(v); err != nil {
			return nil, nil, err
		}
	}
	own := f["bindings"]
	if own != nil && (own.Kind != yaml.SequenceNode || len(own.Content) == 0) {
		return nil, nil, fmt.Errorf("line %d: bindings must be a non-empty list", own.Line)
	}
	v := f["include"]
	if v == nil {
		return nil, own, nil
	}
	if _, err := strList(v, "include"); err != nil {
		return nil, nil, err
	}
	return v.Content, own, nil
}

func (c *Config) decodeBudget(n *yaml.Node) error {
	f, err := fields(n, "max-chars")
	if err != nil {
		return err
	}
	v := f["max-chars"]
	if v == nil {
		return nil
	}
	m, err := strconv.Atoi(v.Value)
	if v.ShortTag() != "!!int" || err != nil || m < 0 {
		return fmt.Errorf("line %d: budget.max-chars must be a whole number, 0 or more", v.Line)
	}
	c.MaxChars = m
	return nil
}

// loadBindings adds the bindings of one binding file. A broken binding is
// skipped; the others in the file still apply. in is the pinned source the
// file is in, or nil.
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
	switch {
	case err != nil:
	case len(docs) == 0:
		err = errors.New("no bindings in this file")
	case len(docs) > 1:
		err = errors.New("a binding file must be a single YAML document: list its bindings under bindings")
	case docs[0].err != nil:
		err = docs[0].err
	}
	var list *yaml.Node
	if err == nil {
		list, err = c.decodeBindingFile(docs[0].node)
	}
	if err != nil {
		c.problem(file, fmt.Errorf("%s; nothing from this file is used", flat(err)))
		return
	}
	c.addBindings(file, list, in)
}

// configOnly are the keys a binding file can't have: only the config chooses
// sources, other files and the budget. A team repository's binding file
// must not.
var configOnly = []string{"sources", "include", "budget"}

// decodeBindingFile checks the top level of a binding file and returns its
// bindings list.
func (c *Config) decodeBindingFile(doc *yaml.Node) (*yaml.Node, error) {
	for i := 0; i+1 < len(doc.Content); i += 2 {
		if k := doc.Content[i]; slices.Contains(configOnly, k.Value) {
			return nil, fmt.Errorf("line %d: %s works only in the config (%s)", k.Line, k.Value, paths.Tilde(c.File))
		}
	}
	f, err := fields(doc, "schema", "bindings")
	if err != nil {
		return nil, err
	}
	if err := checkSchema(doc, f); err != nil {
		return nil, err
	}
	v := f["bindings"]
	if v == nil || v.Kind != yaml.SequenceNode || len(v.Content) == 0 {
		return nil, fmt.Errorf("line %d: bindings must be a non-empty list", doc.Line)
	}
	return v, nil
}

// addBindings adds the bindings in list, an entry of file. Names are unique
// in a file.
func (c *Config) addBindings(file string, list *yaml.Node, in *Source) {
	names := map[string]bool{}
	for _, n := range list.Content {
		b, err := c.decodeBinding(n, file, in)
		if err == nil && names[b.Name] {
			err = fmt.Errorf("line %d: duplicate name %q", n.Line, b.Name)
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

func (c *Config) decodeBinding(n *yaml.Node, file string, in *Source) (Binding, error) {
	if n.Kind == yaml.ScalarNode {
		return Binding{}, fmt.Errorf("line %d: a binding is a map with name, match, instructions and skills; a binding file goes under include, in the config", n.Line)
	}
	f, err := fields(n, "name", "match", "instructions", "skills")
	if err != nil {
		return Binding{}, err
	}
	if err := require(n, f, "name", "match"); err != nil {
		return Binding{}, err
	}
	b := Binding{Source: file, Line: n.Line}
	if b.Name, err = str(f["name"], "name"); err != nil {
		return Binding{}, err
	}
	if !reName.MatchString(b.Name) {
		return Binding{}, fmt.Errorf("line %d: name %q may only contain letters, digits, '.', '_' and '-'", f["name"].Line, b.Name)
	}
	if f["instructions"] == nil && f["skills"] == nil {
		return Binding{}, fmt.Errorf("line %d: a binding needs instructions, skills or both", n.Line)
	}
	if b.Selector, err = decodeSelector(f["match"]); err != nil {
		return Binding{}, err
	}
	if v := f["instructions"]; v != nil {
		if b.Instructions, err = c.pathList(v, "instructions", file, in); err != nil {
			return Binding{}, err
		}
	}
	if v := f["skills"]; v != nil {
		if b.Skills, err = c.pathList(v, "skills", file, in); err != nil {
			return Binding{}, err
		}
	}
	return b, nil
}

// checkSchema checks the schema of a config or binding file.
func checkSchema(doc *yaml.Node, f map[string]*yaml.Node) error {
	if err := require(doc, f, "schema"); err != nil {
		return err
	}
	if v := f["schema"]; v.Kind != yaml.ScalarNode || v.Value != Schema {
		return fmt.Errorf("line %d: schema must be %s (found %q)", v.Line, Schema, v.Value)
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
		r, _, err := c.ref(p, base, in)
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
