package config

import (
	"bytes"
	"fmt"
	"strings"

	"go.yaml.in/yaml/v3"
)

// The commands that add and remove MCP servers write the provisional
// format only through these functions, so that a new config format changes
// one place.

// NewMCPServer is a server for AddMCPServer, with its values as they are
// written in the config.
type NewMCPServer struct {
	Name          string
	URL           string
	Command       []string
	Env           [][2]string
	EnvPass       []string
	SecretEnv     []string
	Headers       [][2]string
	SecretHeaders []string
	ClientID      string
	Scopes        []string
	Notes         string
}

// node is s as a YAML node, its keys in the order the docs show them.
func (s NewMCPServer) node() *yaml.Node {
	str := func(v string) *yaml.Node { return &yaml.Node{Kind: yaml.ScalarNode, Value: v, Tag: "!!str"} }
	list := func(vals []string) *yaml.Node {
		n := &yaml.Node{Kind: yaml.SequenceNode, Style: yaml.FlowStyle}
		for _, v := range vals {
			n.Content = append(n.Content, str(v))
		}
		return n
	}
	pairs := func(kv [][2]string) *yaml.Node {
		n := &yaml.Node{Kind: yaml.MappingNode, Style: yaml.FlowStyle}
		for _, p := range kv {
			n.Content = append(n.Content, str(p[0]), str(p[1]))
		}
		return n
	}
	n := &yaml.Node{Kind: yaml.MappingNode}
	if s.URL != "" {
		set(n, "url", str(s.URL))
	}
	if len(s.Command) > 0 {
		set(n, "command", list(s.Command))
	}
	if len(s.Env) > 0 {
		set(n, "env", pairs(s.Env))
	}
	for _, l := range []struct {
		key  string
		vals []string
	}{{"env-pass", s.EnvPass}, {"secret-env", s.SecretEnv}} {
		if len(l.vals) > 0 {
			set(n, l.key, list(l.vals))
		}
	}
	if len(s.Headers) > 0 {
		set(n, "headers", pairs(s.Headers))
	}
	if len(s.SecretHeaders) > 0 {
		set(n, "secret-headers", list(s.SecretHeaders))
	}
	if s.ClientID != "" {
		set(n, "client-id", str(s.ClientID))
	}
	if len(s.Scopes) > 0 {
		set(n, "scopes", list(s.Scopes))
	}
	if s.Notes != "" {
		set(n, "notes", str(s.Notes))
	}
	return n
}

// text is s as YAML lines under mcp-servers, indented by indent.
func (s NewMCPServer) text(indent string) (string, error) {
	var b bytes.Buffer
	e := yaml.NewEncoder(&b)
	e.SetIndent(2)
	doc := &yaml.Node{Kind: yaml.MappingNode}
	set(doc, s.Name, s.node())
	if err := e.Encode(doc); err != nil {
		return "", err
	}
	if err := e.Close(); err != nil {
		return "", err
	}
	var out strings.Builder
	for _, l := range strings.SplitAfter(b.String(), "\n") {
		if l != "" {
			out.WriteString(indent + l)
		}
	}
	return out.String(), nil
}

// data is s as yaml.Unmarshal reads it.
func (s NewMCPServer) data() any {
	var v any
	b, _ := yaml.Marshal(s.node())
	_ = yaml.Unmarshal(b, &v)
	return v
}

// AddMCPServer adds the server s to mcp-servers. It doesn't check s: the
// caller does, with the config as a session reads it.
func AddMCPServer(data []byte, s NewMCPServer) ([]byte, error) {
	doc, err := root(data)
	if err != nil {
		return nil, err
	}
	servers := get(doc, "mcp-servers")
	if get(servers, s.Name) != nil {
		return nil, fmt.Errorf("an MCP server named %s already exists", s.Name)
	}
	want := func(m map[string]any) {
		if m["mcp-servers"] == nil {
			m["mcp-servers"] = map[string]any{}
		}
		m["mcp-servers"].(map[string]any)[s.Name] = s.data()
	}
	var out []byte
	ok := false
	switch {
	case servers == nil:
		text, err := s.text("  ")
		if err != nil {
			return nil, err
		}
		out, ok = appendText(data, "mcp-servers:\n"+text), true
	case servers.Kind == yaml.MappingNode && servers.Style&yaml.FlowStyle == 0 && len(servers.Content) > 0:
		k := servers.Content[len(servers.Content)-2]
		text, err := s.text(strings.Repeat(" ", k.Column-1))
		if err != nil {
			return nil, err
		}
		out, ok = insertAfter(data, lastLine(servers), text), true
	}
	if ok && same(data, out, want) {
		return out, nil
	}
	return reencode(doc, func() {
		if servers == nil || servers.Kind != yaml.MappingNode {
			servers = &yaml.Node{Kind: yaml.MappingNode}
			set(doc, "mcp-servers", servers)
		}
		set(servers, s.Name, s.node())
	})
}

// RemoveMCPServer removes the server name, and mcp-servers itself when it
// becomes empty.
func RemoveMCPServer(data []byte, name string) ([]byte, error) {
	doc, err := root(data)
	if err != nil {
		return nil, err
	}
	servers := get(doc, "mcp-servers")
	if get(servers, name) == nil {
		return nil, fmt.Errorf("no MCP server %q in the config", name)
	}
	last := len(servers.Content) == 2
	want := func(m map[string]any) {
		if last {
			delete(m, "mcp-servers")
		} else {
			delete(m["mcp-servers"].(map[string]any), name)
		}
	}
	// Its own lines, as far as its nodes go: a value that ends below its
	// last node (a flow list over lines) fails same, and is encoded again.
	if from, to, ok := mcpEntryLines(servers, name); ok {
		if last {
			from = keyLine(doc, "mcp-servers")
		}
		if out := deleteLines(data, from, to); same(data, out, want) {
			return out, nil
		}
	}
	return reencode(doc, func() {
		if last {
			unset(doc, "mcp-servers")
		} else {
			unset(servers, name)
		}
	})
}

// mcpEntryLines are the first and last line of the entry name in the block
// map m.
func mcpEntryLines(m *yaml.Node, name string) (int, int, bool) {
	if m == nil || m.Kind != yaml.MappingNode || m.Style&yaml.FlowStyle != 0 {
		return 0, 0, false
	}
	for i := 0; i+1 < len(m.Content); i += 2 {
		if k, v := m.Content[i], m.Content[i+1]; k.Value == name {
			return k.Line, lastLine(v), true
		}
	}
	return 0, 0, false
}
