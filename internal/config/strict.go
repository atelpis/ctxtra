package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"

	"go.yaml.in/yaml/v3"
)

// The YAML is checked strictly, so that a typo or a missing value can never
// make a binding match more than intended.

// document is one parsed YAML document, or why it can't be used.
type document struct {
	node *yaml.Node
	err  error
}

// parseDocs parses every YAML document in data. Empty documents are skipped.
// A syntax error fails the whole stream: the parser can't tell where the next
// document starts. A document that parses but isn't a map, or uses anchors or
// aliases, gets its own error, and the others still apply.
func parseDocs(data []byte) ([]document, error) {
	dec := yaml.NewDecoder(bytes.NewReader(data))
	var docs []document
	for {
		var root yaml.Node
		err := dec.Decode(&root)
		if errors.Is(err, io.EOF) {
			return docs, nil
		}
		if err != nil {
			return nil, errors.New(flat(err))
		}
		if len(root.Content) == 0 {
			continue
		}
		doc := root.Content[0]
		switch {
		case doc.Kind == yaml.ScalarNode && doc.ShortTag() == "!!null":
			continue // an empty document, e.g. after a trailing ---
		case usesAliases(&root):
			docs = append(docs, document{doc, fmt.Errorf("line %d: YAML anchors and aliases are not supported", doc.Line)})
		case doc.Kind != yaml.MappingNode:
			docs = append(docs, document{doc, fmt.Errorf("line %d: expected a map at the top level", doc.Line)})
		default:
			docs = append(docs, document{node: doc})
		}
	}
}

// usesAliases reports anchors or aliases: an alias can hide a null value from
// validation.
func usesAliases(n *yaml.Node) bool {
	if n.Kind == yaml.AliasNode || n.Anchor != "" {
		return true
	}
	return slices.ContainsFunc(n.Content, usesAliases)
}

// fields checks that n is a map with only allowed keys, no duplicates and no
// null values, and returns the values by key.
func fields(n *yaml.Node, allowed ...string) (map[string]*yaml.Node, error) {
	if n.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("line %d: expected a map", n.Line)
	}
	out := map[string]*yaml.Node{}
	for i := 0; i+1 < len(n.Content); i += 2 {
		k, v := n.Content[i], n.Content[i+1]
		if !slices.Contains(allowed, k.Value) {
			return nil, fmt.Errorf("line %d: unknown key %q (allowed: %s)", k.Line, k.Value, strings.Join(allowed, ", "))
		}
		if _, dup := out[k.Value]; dup {
			return nil, fmt.Errorf("line %d: duplicate key %q", k.Line, k.Value)
		}
		// The decoder would skip a null value, so `remote: ~` would silently
		// drop a condition.
		if v.Kind == yaml.ScalarNode && v.ShortTag() == "!!null" {
			return nil, fmt.Errorf("line %d: %s has no value", k.Line, k.Value)
		}
		out[k.Value] = v
	}
	return out, nil
}

// require reports the first key of keys that is missing from f.
func require(n *yaml.Node, f map[string]*yaml.Node, keys ...string) error {
	for _, k := range keys {
		if f[k] == nil {
			return fmt.Errorf("line %d: %s is required", n.Line, k)
		}
	}
	return nil
}

func str(n *yaml.Node, what string) (string, error) {
	if n.Kind != yaml.ScalarNode || n.ShortTag() != "!!str" || n.Value == "" {
		return "", fmt.Errorf("line %d: %s must be a non-empty string", n.Line, what)
	}
	return n.Value, nil
}

// strList accepts a non-empty list of non-empty strings.
func strList(n *yaml.Node, what string) ([]string, error) {
	if n.Kind != yaml.SequenceNode {
		return nil, fmt.Errorf("line %d: %s must be a list", n.Line, what)
	}
	if len(n.Content) == 0 {
		return nil, fmt.Errorf("line %d: %s is an empty list", n.Line, what)
	}
	out := make([]string, 0, len(n.Content))
	for _, e := range n.Content {
		s, err := str(e, what+" entry")
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, nil
}

func flat(err error) string {
	s := strings.ReplaceAll(err.Error(), "yaml: unmarshal errors:\n", "")
	return strings.Join(strings.Fields(s), " ")
}
