package config

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/atelpis/ctxtra/internal/fsutil"
	"github.com/atelpis/ctxtra/internal/paths"
)

// The commands that change the config (add, sync) edit it as text where
// they can, so that comments, blank lines and alignment stay as the user
// wrote them: they replace one value or append lines. Each text edit is
// checked by parsing the result and comparing it with the intended change;
// when the check fails, or the YAML uses a style the text edit doesn't
// handle (flow maps, block scalars), the whole file is encoded again from
// its YAML nodes instead. That keeps comments, but not blank lines or
// alignment.

// NewConfig is the content of a new config file.
const NewConfig = "apiVersion: " + APIVersion + "\nkind: " + KindConfig + "\n"

// Edit changes the config at path under a lock. fn gets the current content
// (nil when there is no file) and returns the new one, which must be a
// valid config. The file is replaced atomically and keeps its mode; a
// symlink stays a symlink. The old content is kept in ~/.ctxtra/backups.
func Edit(path string, fn func(old []byte) ([]byte, error)) error {
	unlock, err := fsutil.Lock(filepath.Join(paths.StateDir(), "config.lock"))
	if err != nil {
		return err
	}
	defer unlock()
	old, err := fsutil.ReadBounded(path, MaxFileBytes)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if len(bytes.TrimSpace(old)) > 0 {
		if err := Check(old); err != nil {
			return fmt.Errorf("%s: %v; fix it first", paths.Tilde(path), err)
		}
	}
	data, err := fn(old)
	if err != nil {
		return err
	}
	if err := Check(data); err != nil {
		return fmt.Errorf("the changed config would be invalid (%v); nothing changed", err)
	}
	_, _, err = fsutil.WriteAtomic(path, data, paths.BackupDir(), false)
	return err
}

// Check reports whether data is a valid config document. Binding files are
// not read.
func Check(data []byte) error {
	docs, err := parseDocs(data)
	if err != nil {
		return err
	}
	if len(docs) != 1 {
		return errors.New("the config must be a single YAML document")
	}
	c := &Config{}
	_, err = c.decode(docs[0])
	return err
}

// Parse decodes the sources of a config, without reading binding files.
// Empty data is an empty config.
func Parse(data []byte) (*Config, error) {
	c := &Config{}
	docs, err := parseDocs(data)
	if err != nil {
		return nil, err
	}
	if len(docs) > 1 {
		return nil, errors.New("the config must be a single YAML document")
	}
	if len(docs) == 1 {
		var entries []*yaml.Node
		entries, err = c.decode(docs[0])
		for _, e := range entries {
			c.BindingEntries = append(c.BindingEntries, e.Value)
		}
	}
	return c, err
}

// SetCommit sets the commit of the source name.
func SetCommit(data []byte, name, commit string) ([]byte, error) {
	doc, err := root(data)
	if err != nil {
		return nil, err
	}
	entry := get(get(doc, "sources"), name)
	if entry == nil || entry.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("no source %q in the config", name)
	}
	val := "'" + commit + "'"
	want := func(m map[string]any) { m["sources"].(map[string]any)[name].(map[string]any)["commit"] = commit }
	if v := get(entry, "commit"); v != nil {
		if out, ok := replaceToken(data, v, val); ok && same(data, out, want) {
			return out, nil
		}
	} else if out, ok := appendToMap(data, entry, [][2]string{{"commit", val}}); ok && same(data, out, want) {
		return out, nil
	}
	return reencode(doc, func() {
		set(entry, "commit", &yaml.Node{Kind: yaml.ScalarNode, Style: yaml.SingleQuotedStyle, Value: commit})
	})
}

// AddSource adds the source name with the given keys and values, in order.
func AddSource(data []byte, name string, kv [][2]string) ([]byte, error) {
	doc, err := root(data)
	if err != nil {
		return nil, err
	}
	want := func(m map[string]any) {
		if m["sources"] == nil {
			m["sources"] = map[string]any{}
		}
		e := map[string]any{}
		for _, p := range kv {
			e[p[0]] = p[1]
		}
		m["sources"].(map[string]any)[name] = e
	}
	quoted := make([][2]string, len(kv))
	for i, p := range kv {
		quoted[i] = [2]string{p[0], scalar(p[1])}
		if p[0] == "commit" {
			quoted[i][1] = "'" + p[1] + "'" // as the docs show it
		}
	}
	sources := get(doc, "sources")
	if sources != nil && get(sources, name) != nil {
		return nil, fmt.Errorf("a source named %s already exists", name)
	}
	var out []byte
	ok := false
	switch {
	case sources == nil:
		var b strings.Builder
		b.WriteString("sources:\n  " + key(name) + ":\n")
		for _, p := range quoted {
			b.WriteString("    " + p[0] + ": " + p[1] + "\n")
		}
		out, ok = appendText(data, b.String()), true
	case sources.Kind == yaml.MappingNode && len(sources.Content) > 0:
		out, ok = appendEntry(data, sources, name, quoted)
	}
	if ok && same(data, out, want) {
		return out, nil
	}
	return reencode(doc, func() {
		if sources == nil {
			sources = &yaml.Node{Kind: yaml.MappingNode}
			set(doc, "sources", sources)
		}
		e := &yaml.Node{Kind: yaml.MappingNode}
		for _, p := range kv {
			v := &yaml.Node{Kind: yaml.ScalarNode, Value: p[1]}
			if p[0] == "commit" {
				v.Style = yaml.SingleQuotedStyle
			}
			set(e, p[0], v)
		}
		set(sources, name, e)
	})
}

// AddBinding appends ref to bindings.
func AddBinding(data []byte, ref string) ([]byte, error) {
	doc, err := root(data)
	if err != nil {
		return nil, err
	}
	want := func(m map[string]any) {
		l, _ := m["bindings"].([]any)
		m["bindings"] = append(l, ref)
	}
	list := get(doc, "bindings")
	var out []byte
	ok := false
	switch {
	case list == nil:
		out, ok = appendText(data, "bindings:\n  - "+scalar(ref)+"\n"), true
	case list.Kind == yaml.SequenceNode && len(list.Content) > 0:
		out, ok = appendItem(data, list, scalar(ref))
	}
	if ok && same(data, out, want) {
		return out, nil
	}
	return reencode(doc, func() {
		if list == nil {
			list = &yaml.Node{Kind: yaml.SequenceNode}
			set(doc, "bindings", list)
		}
		list.Content = append(list.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: ref})
	})
}

// root parses data and returns the top-level map. Empty data is a new
// config.
func root(data []byte) (*yaml.Node, error) {
	if len(bytes.TrimSpace(data)) == 0 {
		data = []byte(NewConfig)
	}
	docs, err := parseDocs(data)
	if err != nil {
		return nil, err
	}
	if len(docs) != 1 || docs[0].err != nil {
		return nil, errors.New("the config must be a single YAML map")
	}
	return docs[0].node, nil
}

func get(m *yaml.Node, k string) *yaml.Node {
	if m == nil || m.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == k {
			return m.Content[i+1]
		}
	}
	return nil
}

func set(m *yaml.Node, k string, v *yaml.Node) {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == k {
			m.Content[i+1] = v
			return
		}
	}
	m.Content = append(m.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: k}, v)
}

// reencode applies change to the node tree of doc and encodes it.
func reencode(doc *yaml.Node, change func()) ([]byte, error) {
	change()
	var b bytes.Buffer
	e := yaml.NewEncoder(&b)
	e.SetIndent(2)
	if err := e.Encode(doc); err != nil {
		return nil, err
	}
	return b.Bytes(), e.Close()
}

// same reports whether after is before with change applied, as data.
func same(before, after []byte, change func(map[string]any)) bool {
	if len(bytes.TrimSpace(before)) == 0 {
		before = []byte(NewConfig)
	}
	var a, b map[string]any
	if yaml.Unmarshal(before, &a) != nil || yaml.Unmarshal(after, &b) != nil || a == nil {
		return false
	}
	defer func() { recover() }() // a change that doesn't fit the data: not the same
	change(a)
	return reflect.DeepEqual(a, b)
}

// scalar is v as a YAML value: plain when YAML reads it back as the same
// string, else in single quotes.
func scalar(v string) string {
	var m map[string]any
	if yaml.Unmarshal([]byte("k: "+v), &m) == nil && m["k"] == v && !strings.ContainsAny(v, "'\"#") {
		return v
	}
	return "'" + strings.ReplaceAll(v, "'", "''") + "'"
}

// key is k as a YAML map key.
func key(k string) string {
	var m map[string]any
	if yaml.Unmarshal([]byte(k+": x"), &m) == nil && m[k] == "x" {
		return k
	}
	return "'" + strings.ReplaceAll(k, "'", "''") + "'"
}

func lines(data []byte) []string { return strings.SplitAfter(string(data), "\n") }

// token is how a one-line scalar is written in the file.
func token(n *yaml.Node) (string, bool) {
	switch n.Style {
	case 0:
		return n.Value, true
	case yaml.SingleQuotedStyle:
		return "'" + strings.ReplaceAll(n.Value, "'", "''") + "'", true
	case yaml.DoubleQuotedStyle:
		if strings.ContainsAny(n.Value, `"\`) {
			return "", false
		}
		return `"` + n.Value + `"`, true
	}
	return "", false
}

// replaceToken replaces the scalar n with val in place.
func replaceToken(data []byte, n *yaml.Node, val string) ([]byte, bool) {
	tok, ok := token(n)
	ls := lines(data)
	if !ok || n.Kind != yaml.ScalarNode || n.Line < 1 || n.Line > len(ls) {
		return nil, false
	}
	line, col := ls[n.Line-1], n.Column-1
	if col < 0 || !strings.HasPrefix(line[min(col, len(line)):], tok) {
		return nil, false
	}
	ls[n.Line-1] = line[:col] + val + line[col+len(tok):]
	return []byte(strings.Join(ls, "")), true
}

// block reports whether n and everything in it is block style with
// one-line scalars, so that its last line is the line of its last node.
func block(n *yaml.Node) bool {
	if n.Style&(yaml.FlowStyle|yaml.LiteralStyle|yaml.FoldedStyle) != 0 {
		return false
	}
	for _, c := range n.Content {
		if !block(c) {
			return false
		}
	}
	return true
}

func lastLine(n *yaml.Node) int {
	l := n.Line
	for _, c := range n.Content {
		l = max(l, lastLine(c))
	}
	return l
}

// insertAfter inserts text after line n (1-based).
func insertAfter(data []byte, n int, text string) []byte {
	ls := lines(data)
	if n > len(ls) {
		return appendText(data, text)
	}
	if n < 1 {
		return append([]byte(text), data...)
	}
	if !strings.HasSuffix(ls[n-1], "\n") {
		ls[n-1] += "\n"
	}
	return []byte(strings.Join(ls[:n], "") + text + strings.Join(ls[n:], ""))
}

func appendText(data []byte, text string) []byte {
	if len(bytes.TrimSpace(data)) == 0 {
		data = []byte(NewConfig)
	}
	if !bytes.HasSuffix(data, []byte("\n")) {
		data = append(data, '\n')
	}
	return append(data, text...)
}

// appendToMap adds keys at the end of the block map m, at its keys'
// indentation.
func appendToMap(data []byte, m *yaml.Node, kv [][2]string) ([]byte, bool) {
	if !block(m) || len(m.Content) == 0 {
		return nil, false
	}
	indent := strings.Repeat(" ", m.Content[0].Column-1)
	var b strings.Builder
	for _, p := range kv {
		b.WriteString(indent + p[0] + ": " + p[1] + "\n")
	}
	return insertAfter(data, lastLine(m), b.String()), true
}

// appendEntry adds a map entry name with the keys kv to the block map m.
// Its last entry must be block style too, so that its last line is known.
func appendEntry(data []byte, m *yaml.Node, name string, kv [][2]string) ([]byte, bool) {
	last := m.Content[len(m.Content)-1]
	if m.Style&yaml.FlowStyle != 0 || !block(last) {
		return nil, false
	}
	indent := m.Content[0].Column - 1
	child := indent + 2
	if last.Kind == yaml.MappingNode && len(last.Content) > 0 {
		child = last.Content[0].Column - 1
	}
	var b strings.Builder
	b.WriteString(strings.Repeat(" ", indent) + key(name) + ":\n")
	for _, p := range kv {
		b.WriteString(strings.Repeat(" ", child) + p[0] + ": " + p[1] + "\n")
	}
	return insertAfter(data, lastLine(m), b.String()), true
}

// appendItem adds val to the list l: a new "- " line after a block list,
// or ", val" before the ] of a one-line flow list.
func appendItem(data []byte, l *yaml.Node, val string) ([]byte, bool) {
	last := l.Content[len(l.Content)-1]
	ls := lines(data)
	if last.Line < 1 || last.Line > len(ls) {
		return nil, false
	}
	line := ls[last.Line-1]
	if l.Style&yaml.FlowStyle != 0 {
		tok, ok := token(last)
		end := last.Column - 1 + len(tok)
		if !ok || l.Line != last.Line || end > len(line) || line[last.Column-1:end] != tok ||
			!strings.HasPrefix(strings.TrimLeft(line[end:], " "), "]") {
			return nil, false
		}
		ls[last.Line-1] = line[:end] + ", " + val + line[end:]
		return []byte(strings.Join(ls, "")), true
	}
	prefix := line[:min(last.Column-1, len(line))]
	if !block(l) || strings.TrimSpace(prefix) != "-" {
		return nil, false
	}
	return insertAfter(data, lastLine(l), prefix+val+"\n"), true
}

// RemoveBinding removes entry from bindings, and bindings itself when it
// becomes empty.
func RemoveBinding(data []byte, entry string) ([]byte, error) {
	doc, err := root(data)
	if err != nil {
		return nil, err
	}
	list := get(doc, "bindings")
	i := -1
	if list != nil {
		i = slices.IndexFunc(list.Content, func(n *yaml.Node) bool { return n.Value == entry })
	}
	if i < 0 {
		return nil, fmt.Errorf("%s is not in bindings", entry)
	}
	last := len(list.Content) == 1
	want := func(m map[string]any) {
		if last {
			delete(m, "bindings")
			return
		}
		l := m["bindings"].([]any)
		m["bindings"] = slices.Delete(slices.Clone(l), i, i+1)
	}
	item := list.Content[i]
	if list.Style&yaml.FlowStyle == 0 && block(list) && alone(data, item) {
		from := item.Line
		if last {
			from = keyLine(doc, "bindings")
		}
		if out := deleteLines(data, from, item.Line); same(data, out, want) {
			return out, nil
		}
	}
	return reencode(doc, func() {
		if last {
			unset(doc, "bindings")
		} else {
			list.Content = slices.Delete(list.Content, i, i+1)
		}
	})
}

// RemoveSource removes the source name, and sources itself when it becomes
// empty.
func RemoveSource(data []byte, name string) ([]byte, error) {
	doc, err := root(data)
	if err != nil {
		return nil, err
	}
	sources := get(doc, "sources")
	if get(sources, name) == nil {
		return nil, fmt.Errorf("no source %q in the config", name)
	}
	last := len(sources.Content) == 2
	want := func(m map[string]any) {
		if last {
			delete(m, "sources")
		} else {
			delete(m["sources"].(map[string]any), name)
		}
	}
	if from, to, ok := entryLines(sources, name); ok {
		if last {
			from = keyLine(doc, "sources")
		}
		if out := deleteLines(data, from, to); same(data, out, want) {
			return out, nil
		}
	}
	return reencode(doc, func() {
		if last {
			unset(doc, "sources")
		} else {
			unset(sources, name)
		}
	})
}

// ReplaceSource replaces the keys of the source name with kv, in place:
// references to the source keep working, with the new kind.
func ReplaceSource(data []byte, name string, kv [][2]string) ([]byte, error) {
	doc, err := root(data)
	if err != nil {
		return nil, err
	}
	sources := get(doc, "sources")
	if get(sources, name) == nil {
		return nil, fmt.Errorf("no source %q in the config", name)
	}
	want := func(m map[string]any) {
		e := map[string]any{}
		for _, p := range kv {
			e[p[0]] = p[1]
		}
		m["sources"].(map[string]any)[name] = e
	}
	if from, to, ok := entryLines(sources, name); ok {
		k := sources.Content[slices.IndexFunc(sources.Content, func(n *yaml.Node) bool { return n.Value == name })]
		indent := strings.Repeat(" ", k.Column-1)
		var b strings.Builder
		b.WriteString(indent + key(name) + ":\n")
		for _, p := range kv {
			b.WriteString(indent + "  " + p[0] + ": " + value(p) + "\n")
		}
		out := insertAfter(deleteLines(data, from, to), from-1, b.String())
		if same(data, out, want) {
			return out, nil
		}
	}
	return reencode(doc, func() {
		e := &yaml.Node{Kind: yaml.MappingNode}
		for _, p := range kv {
			set(e, p[0], &yaml.Node{Kind: yaml.ScalarNode, Value: p[1]})
		}
		set(sources, name, e)
	})
}

// value is a key's value as YAML text: commits in quotes, as the docs show
// them.
func value(p [2]string) string {
	if p[0] == "commit" {
		return "'" + p[1] + "'"
	}
	return scalar(p[1])
}

// entryLines are the first and last line of the entry name in the block map
// m, when the text edit can handle it.
func entryLines(m *yaml.Node, name string) (int, int, bool) {
	if m == nil || m.Style&yaml.FlowStyle != 0 {
		return 0, 0, false
	}
	for i := 0; i+1 < len(m.Content); i += 2 {
		k, v := m.Content[i], m.Content[i+1]
		if k.Value != name {
			continue
		}
		if v.Style&yaml.FlowStyle != 0 {
			// A one-line flow entry: name: {git: ...}
			if lastLine(v) == k.Line && (i+2 >= len(m.Content) || m.Content[i+2].Line > k.Line) {
				return k.Line, k.Line, true
			}
			return 0, 0, false
		}
		if !block(v) {
			return 0, 0, false
		}
		return k.Line, lastLine(v), true
	}
	return 0, 0, false
}

// keyLine is the line of the top-level key k.
func keyLine(doc *yaml.Node, k string) int {
	for i := 0; i+1 < len(doc.Content); i += 2 {
		if doc.Content[i].Value == k {
			return doc.Content[i].Line
		}
	}
	return 0
}

// alone reports whether the list item n is the only thing on its line.
func alone(data []byte, n *yaml.Node) bool {
	tok, ok := token(n)
	ls := lines(data)
	if !ok || n.Line < 1 || n.Line > len(ls) {
		return false
	}
	line := ls[n.Line-1]
	start := n.Column - 1
	if start > len(line) || strings.TrimSpace(line[:start]) != "-" || !strings.HasPrefix(line[start:], tok) {
		return false
	}
	rest := strings.TrimSpace(line[start+len(tok):])
	return rest == "" || strings.HasPrefix(rest, "#")
}

// deleteLines removes the lines from to to (1-based, inclusive).
func deleteLines(data []byte, from, to int) []byte {
	ls := lines(data)
	if from < 1 || to > len(ls) || from > to {
		return data
	}
	return []byte(strings.Join(ls[:from-1], "") + strings.Join(ls[to:], ""))
}

func unset(m *yaml.Node, k string) {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == k {
			m.Content = slices.Delete(m.Content, i, i+2)
			return
		}
	}
}
