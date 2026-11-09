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

	"github.com/bmatcuk/doublestar/v4"
	"go.yaml.in/yaml/v3"

	"github.com/ctxtra/ctxtra/internal/mcp/upstream"
	"github.com/ctxtra/ctxtra/internal/paths"
	"github.com/ctxtra/ctxtra/internal/state"
	"github.com/ctxtra/ctxtra/internal/trust"
)

const (
	// Schema is the only accepted schema, in the config and in binding
	// files.
	Schema = "ctxtra/v1"

	// MaxFileBytes limits every file a session reads: 1,000,000 characters,
	// which an agent can get in full, are up to 4 MB of UTF-8.
	MaxFileBytes = 4 << 20
	// MaxSessionBytes limits what one session reads in total: the config,
	// binding files, instruction files and SKILL.md files.
	MaxSessionBytes = 16 << 20

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
	Includes []string
	Bindings []Binding // the config's own, then those of each binding file
	MaxChars int       // budget.max-chars, in characters (Unicode code points); 0 means no limit
	// CheckCtxtra is check-updates: check for new ctxtra releases.
	CheckCtxtra bool
	Summary     SummaryMessage
	Problems    []string
	Warnings    []string // only from LoadSource: problems that leave the binding in use
	Notes       []string // hints, for doctor
	// MCPServers are the valid servers of mcp-servers, in config order.
	MCPServers  []upstream.Server
	mcpBroken   map[string]bool   // names in mcp-servers with an error
	pinned      map[string]string // paths resolved into the store, and their references
	mappings    *state.Mappings   // of imported clones; read when needed
	mappingsErr error
	touched     map[string]bool // generations touched for this load
	storeBusy   error           // a touch failed: don't wait again
	waitLeft    time.Duration   // what touches may still wait for the store
	waitSet     bool
	checking    bool // loaded by LoadSource
	ignored     bool // see Ignored
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
	MCPServers   []string // names of servers in the config's mcp-servers
	// Paths that entries starting with ! removed from Instructions and Skills.
	ExcludedInstructions, ExcludedSkills []string
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
	c := &Config{File: path, CheckCtxtra: true, Summary: DefaultSummaryMessage}
	c.Trust = c.policy()
	data, err := rd.Read(path)
	if errors.Is(err, os.ErrNotExist) {
		return c
	}
	if err != nil {
		return c.ignore(err)
	}
	docs, err := parseDocs(data)
	if err != nil {
		return c.ignore(err)
	}
	switch {
	case len(docs) == 0:
		return c
	case len(docs) > 1:
		return c.ignore(errors.New("the config must be a single YAML document"))
	}
	// The whole config is checked before any binding is used: with a broken
	// budget or source, using the bindings would add more than intended.
	includes, own, err := c.decode(docs[0])
	if err != nil {
		return c.ignore(fmt.Errorf("%s; nothing from this file is used", flat(err)))
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

// ignore reports a config that can't be used as a whole. Its
// check-updates: false may be what failed, so no update check runs either.
func (c *Config) ignore(err error) *Config {
	c.problem(c.File, err)
	c.ignored, c.CheckCtxtra, c.Summary = true, false, DefaultSummaryMessage
	return c
}

// Ignored reports whether the config exists but none of it is used.
func (c *Config) Ignored() bool { return c.ignored }

// decode checks the config document and sets the sources and the budget.
// It returns the include entries and the config's own bindings list (nil
// without one). The bindings themselves are checked one by one later: a
// broken binding is skipped alone.
func (c *Config) decode(d document) ([]*yaml.Node, *yaml.Node, error) {
	if d.err != nil {
		return nil, nil, d.err
	}
	keys := []string{"schema", "check-updates", "summary-message", "sources", "include", "bindings", "budget"}
	if mcpOn() {
		keys = append(keys, "mcp-servers")
	}
	f, err := fields(c.withoutNullSummary(d.node), keys...)
	if err != nil {
		return nil, nil, err
	}
	if err := checkSchema(d.node, f); err != nil {
		return nil, nil, err
	}
	if v := f["mcp-servers"]; v != nil {
		c.decodeMCPServers(v)
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
	if v := f["check-updates"]; v != nil {
		if c.CheckCtxtra, err = boolean(v, "check-updates"); err != nil {
			return nil, nil, err
		}
	}
	if v := f["summary-message"]; v != nil {
		c.decodeSummary(v)
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
	if v := f["max-chars"]; v != nil {
		c.MaxChars, err = count(v, "budget.max-chars")
	}
	return err
}

// withoutNullSummary is n without a summary-message that has no value,
// which it reports as a problem: fields would refuse the whole config.
func (c *Config) withoutNullSummary(n *yaml.Node) *yaml.Node {
	if n.Kind != yaml.MappingNode {
		return n
	}
	for i := 0; i+1 < len(n.Content); i += 2 {
		k, v := n.Content[i], n.Content[i+1]
		if k.Value == "summary-message" && isNull(v) {
			c.problem(c.File, fmt.Errorf("line %d: summary-message has no value; the defaults apply", k.Line))
			out := *n
			out.Content = slices.Delete(slices.Clone(n.Content), i, i+2)
			return &out
		}
	}
	return n
}

// SummaryMessage is summary-message: what the session summary lists.
type SummaryMessage struct {
	ShowInstructions, ShowSkills bool
	// MaxInstructions and MaxSkills are the most entries listed for each
	// source; 0 lists only the number of entries.
	MaxInstructions, MaxSkills int
}

// DefaultSummaryMessage applies to each key of summary-message that the
// config doesn't set. The limits keep a broken setup, one that adds
// hundreds of files, from filling the screen.
var DefaultSummaryMessage = SummaryMessage{ShowInstructions: true, ShowSkills: true, MaxInstructions: 30, MaxSkills: 30}

// decodeSummary sets c.Summary. Only the summary depends on it: a wrong key
// or value is a problem, and the default applies to that key alone; the
// rest of the config is still used.
func (c *Config) decodeSummary(n *yaml.Node) {
	if n.Kind != yaml.MappingNode {
		c.problem(c.File, fmt.Errorf("line %d: summary-message must be a map; the defaults apply", n.Line))
		return
	}
	s, d, seen := &c.Summary, DefaultSummaryMessage, map[string]bool{}
	keys := map[string]func(k, v *yaml.Node) error{
		"show-instructions": func(k, v *yaml.Node) error {
			return summaryKey(&s.ShowInstructions, d.ShowInstructions, k, v, seen, boolean)
		},
		"show-skills": func(k, v *yaml.Node) error { return summaryKey(&s.ShowSkills, d.ShowSkills, k, v, seen, boolean) },
		"max-instructions": func(k, v *yaml.Node) error {
			return summaryKey(&s.MaxInstructions, d.MaxInstructions, k, v, seen, count)
		},
		"max-skills": func(k, v *yaml.Node) error { return summaryKey(&s.MaxSkills, d.MaxSkills, k, v, seen, count) },
	}
	allowed := []string{"show-instructions", "show-skills", "max-instructions", "max-skills"}
	for i := 0; i+1 < len(n.Content); i += 2 {
		k, v := n.Content[i], n.Content[i+1]
		var err error
		if set := keys[k.Value]; set != nil {
			err = set(k, v)
		} else {
			err = fmt.Errorf("line %d: unknown key %q in summary-message (allowed: %s); it is ignored", k.Line, k.Value, strings.Join(allowed, ", "))
		}
		if err != nil {
			c.problem(c.File, err)
		}
	}
}

// summaryKey sets *p to the value v of the key k of summary-message, or to
// def when v is wrong or k is there twice.
func summaryKey[T any](p *T, def T, k, v *yaml.Node, seen map[string]bool, read func(*yaml.Node, string) (T, error)) error {
	what := "summary-message." + k.Value
	var err error
	switch x, rerr := read(v, what); {
	case seen[k.Value]:
		err = fmt.Errorf("line %d: duplicate key %q in summary-message", k.Line, k.Value)
	case isNull(v):
		err = fmt.Errorf("line %d: %s has no value", k.Line, what)
	case rerr != nil:
		err = rerr
	default:
		*p = x
	}
	seen[k.Value] = true
	if err != nil {
		*p = def
		return fmt.Errorf("%w; the default, %v, applies", err, def)
	}
	return nil
}

func isNull(v *yaml.Node) bool { return v.Kind == yaml.ScalarNode && v.ShortTag() == "!!null" }

// count reads a whole number, 0 or more.
func count(v *yaml.Node, what string) (int, error) {
	n, err := strconv.Atoi(v.Value)
	if v.ShortTag() != "!!int" || err != nil || n < 0 {
		return 0, fmt.Errorf("line %d: %s must be a whole number, 0 or more", v.Line, what)
	}
	return n, nil
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
// sources, other files, the budget, update checks and the summary. A source repository's
// binding file must not.
var configOnly = []string{"check-updates", "summary-message", "sources", "include", "budget"}

// decodeBindingFile checks the top level of a binding file and returns its
// bindings list.
func (c *Config) decodeBindingFile(doc *yaml.Node) (*yaml.Node, error) {
	only := configOnly
	if mcpOn() {
		only = append(slices.Clip(only), "mcp-servers")
	}
	for i := 0; i+1 < len(doc.Content); i += 2 {
		if k := doc.Content[i]; slices.Contains(only, k.Value) {
			if c.File == "" {
				return nil, fmt.Errorf("line %d: %s works only in the config, not in a binding file", k.Line, k.Value)
			}
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
	keys := []string{"name", "match", "instructions", "skills"}
	if mcpOn() {
		keys = append(keys, "mcp-servers")
	}
	f, err := fields(n, keys...)
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
	if f["instructions"] == nil && f["skills"] == nil && f["mcp-servers"] == nil {
		if mcpOn() {
			return Binding{}, fmt.Errorf("line %d: a binding needs instructions, skills or mcp-servers", n.Line)
		}
		return Binding{}, fmt.Errorf("line %d: a binding needs instructions, skills or both", n.Line)
	}
	if b.Selector, err = decodeSelector(f["match"]); err != nil {
		return Binding{}, err
	}
	if v := f["instructions"]; v != nil {
		if b.Instructions, b.ExcludedInstructions, err = c.pathList(v, "instructions", file, in); err != nil {
			return Binding{}, err
		}
	}
	if v := f["skills"]; v != nil {
		if b.Skills, b.ExcludedSkills, err = c.pathList(v, "skills", file, in); err != nil {
			return Binding{}, err
		}
	}
	if v := f["mcp-servers"]; v != nil {
		if b.MCPServers, err = c.mcpNames(v, file, in); err != nil {
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

// pathList resolves and expands the paths listed in file. A reference to
// a pinned source that isn't available is skipped alone, with a problem.
// An entry that starts with ! removes the paths it matches from what the
// other entries add, whatever its position; the removed paths are returned
// too.
func (c *Config) pathList(n *yaml.Node, what, file string, in *Source) (out, excluded []string, err error) {
	list, err := strList(n, what)
	if err != nil {
		return nil, nil, err
	}
	base := filepath.Dir(file)
	var excludes []exclusion
	adds := 0
	for _, p := range list {
		if c.checking && in != nil && strings.HasPrefix(strings.TrimPrefix(p, "!"), Scheme+in.Name+"/") {
			c.warn(file, fmt.Errorf("line %d: %s works only where the source is named %s; a relative path works under any name", n.Line, p, in.Name))
		}
		if rest, ok := strings.CutPrefix(p, "!"); ok {
			if rest == "" {
				return nil, nil, fmt.Errorf("line %d: %s entry %q is only a !: write the path to leave out after it", n.Line, what, p)
			}
			// An exclusion that can't be resolved skips the binding: without
			// it, the binding would add more than intended.
			x, err := c.exclusion(rest, what, base, in)
			if err != nil {
				return nil, nil, fmt.Errorf("line %d: %s entry %q: %w", n.Line, what, p, err)
			}
			x.entry = p
			excludes = append(excludes, x)
			continue
		}
		adds++
		r, src, pattern, err := c.entry(p, base, in, true)
		var unavailable errUnavailable
		switch {
		case errors.As(err, &unavailable):
			c.problem(file, fmt.Errorf("line %d: %w", n.Line, err))
			continue
		case err != nil:
			return nil, nil, fmt.Errorf("line %d: %w", n.Line, err)
		}
		if pattern == "" {
			out = append(out, r)
			continue
		}
		matches, err := c.expand(r, pattern, what, src)
		if err != nil {
			err = fmt.Errorf("line %d: %s entry %q (in %q): %w", n.Line, what, p, paths.Tilde(r), err)
			if errors.As(err, new(noMatch)) {
				c.warn(file, err)
			} else {
				c.problem(file, err)
			}
			continue
		}
		out = append(out, matches...)
	}
	if adds == 0 {
		return nil, nil, fmt.Errorf("line %d: %s has only entries that start with !, which remove paths that the other entries add", n.Line, what)
	}
	if len(out) == 0 {
		return nil, nil, nil
	}
	for _, x := range excludes {
		n0 := len(out)
		out = slices.DeleteFunc(out, func(p string) bool {
			if x.match(p) {
				excluded = append(excluded, p)
				return true
			}
			return false
		})
		if len(out) == n0 && !slices.ContainsFunc(excluded, x.match) {
			c.warn(file, fmt.Errorf("line %d: %s entry %q excludes nothing", n.Line, what, x.entry))
		}
	}
	return out, excluded, nil
}

// entry resolves the path p, written in a file in base. A glob resolves to
// its fixed folder and the pattern below it; a plain path has no pattern.
// follow is passed to refLinks.
func (c *Config) entry(p, base string, in *Source, follow bool) (r string, src *Source, pattern string, err error) {
	if !strings.ContainsAny(p, "*?[{") {
		r, src, err = c.refLinks(p, base, in, follow)
		return r, src, "", err
	}
	if !doublestar.ValidatePattern(filepath.ToSlash(p)) {
		return "", nil, "", fmt.Errorf("invalid glob pattern %q", p)
	}
	prefix, pattern := doublestar.SplitPattern(filepath.ToSlash(p))
	// Resolve a child of the fixed prefix so a pattern at a source's top
	// also passes ref's requirement for a path inside the source.
	ref := strings.TrimSuffix(prefix, "/") + "/.ctxtra-glob-root"
	notesBefore := len(c.Notes)
	r, src, err = c.refLinks(ref, base, in, follow)
	c.Notes = c.Notes[:notesBefore]
	if err != nil {
		var unavailable errUnavailable
		err2 := errors.New(strings.ReplaceAll(err.Error(), ref, p))
		if errors.As(err, &unavailable) {
			err2 = errUnavailable{err2}
		}
		return "", nil, "", err2
	}
	return filepath.Dir(r), src, pattern, nil
}

// exclusion is an entry that starts with !: a path, or a pattern below
// root.
type exclusion struct {
	entry   string
	root    string
	pattern string // "" for a plain path: root is the path
	dirs    bool   // skill folders, which a pattern ending in / matches
}

func (c *Config) exclusion(p, what, base string, in *Source) (exclusion, error) {
	r, _, pattern, err := c.entry(p, base, in, false)
	if err == nil {
		err = checkParents(pattern)
	}
	if err != nil {
		return exclusion{}, err
	}
	return exclusion{root: r, pattern: pattern, dirs: what == "skills"}, nil
}

// match reports whether x removes p. Paths are compared as text, without
// following symlinks: a symlink in someone else's repository must not
// choose what is left out.
func (x exclusion) match(p string) bool {
	if x.pattern == "" {
		return filepath.Clean(p) == filepath.Clean(x.root)
	}
	// As in expansion, a pattern matches below its fixed folder, not the
	// folder itself.
	rel, ok := paths.Rel(x.root, p)
	return ok && rel != "." && (globMatch(x.pattern, rel) || x.dirs && globMatch(x.pattern, rel+"/"))
}

func (c *Config) problem(file string, err error) {
	c.Problems = append(c.Problems, paths.Tilde(file)+": "+flat(err))
}

// warn is problem for what leaves the binding in use. Only source check
// tells it apart.
func (c *Config) warn(file string, err error) {
	if !c.checking {
		c.problem(file, err)
		return
	}
	c.Warnings = append(c.Warnings, paths.Tilde(file)+": "+flat(err))
}
