package dsh

import (
	"encoding/json"
	"errors"
	"strings"

	"go.yaml.in/yaml/v3"
)

// The row that loads the plugin is kept between two comment lines, so setup
// finds and replaces it without rewriting the user's own patches, comments
// and !!js expressions.
const (
	beginMark = "# >>> ctxtra: loads ctxtra's plugin; `ctxtra setup --remove` removes it"
	endMark   = "# <<< ctxtra"
	rowID     = "ctxtra"
)

// patchBlock is the patch entry that inserts the row for plugin.
func patchBlock(plugin string) string {
	name, _ := json.Marshal(plugin) // a JSON string is a YAML double-quoted string
	return beginMark + "\n- insert:\n    - id: " + rowID + "\n      name: " + string(name) + "\n" + endMark + "\n"
}

// editPatch returns the patch file text with our entry set to block, or
// removed when block is "". The file must stay a top-level YAML list: DSH
// refuses to start with anything else, an empty file included.
func editPatch(old, block string) (string, error) {
	before, after, found, err := cutBlock(old)
	if err != nil {
		return "", err
	}
	var out string
	switch {
	case found:
		out = before + block + after
	case block == "":
		return old, nil
	default:
		rest, err := withoutEmptyList(old)
		if err != nil {
			return "", err
		}
		if rest != "" && !strings.HasSuffix(rest, "\n") {
			rest += "\n"
		}
		out = rest + block
	}
	root, err := parse(out)
	if err != nil {
		return "", err
	}
	if root == nil {
		if out != "" && !strings.HasSuffix(out, "\n") {
			out += "\n"
		}
		out += "[]\n"
	} else if root.Kind != yaml.SequenceNode {
		return "", errors.New("not a YAML list")
	}
	return out, nil
}

// cutBlock splits text around our block, marker lines included.
func cutBlock(text string) (before, after string, found bool, err error) {
	start := lineIndex(text, beginMark, 0)
	if start < 0 {
		return text, "", false, nil
	}
	end := lineIndex(text, endMark, start)
	if end < 0 {
		return "", "", false, errors.New("ctxtra's entry has no end line " + endMark)
	}
	end += len(endMark)
	if end < len(text) && text[end] == '\n' {
		end++
	}
	return text[:start], text[end:], true, nil
}

// lineIndex is the offset of the first line at or after from that is
// exactly line, or -1.
func lineIndex(text, line string, from int) int {
	for i := from; i < len(text); {
		rest := text[i:]
		l, _, _ := strings.Cut(rest, "\n")
		if strings.TrimRight(l, "\r") == line {
			return i
		}
		i += len(l) + 1
	}
	return -1
}

// withoutEmptyList returns text ready for a block list entry to be
// appended: a flow-style empty list ("[]", which DSH writes into new patch
// files) is removed. A list with entries must be in block style.
func withoutEmptyList(text string) (string, error) {
	root, err := parse(text)
	if err != nil || root == nil {
		return text, err
	}
	if root.Kind != yaml.SequenceNode {
		return "", errors.New("not a YAML list")
	}
	if root.Style&yaml.FlowStyle == 0 {
		return text, nil
	}
	lines := strings.SplitAfter(text, "\n")
	if len(root.Content) > 0 || root.Line < 1 || root.Line > len(lines) {
		return "", errors.New("a flow-style list ([...]) with entries")
	}
	code, _, _ := strings.Cut(lines[root.Line-1], "#")
	if strings.TrimSpace(code) != "[]" {
		return "", errors.New("a flow-style list ([...]) that shares its line")
	}
	return strings.Join(append(lines[:root.Line-1:root.Line-1], lines[root.Line:]...), ""), nil
}

// parse returns the top-level node of text, or nil for a file without one.
func parse(text string) (*yaml.Node, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(text), &doc); err != nil {
		return nil, err
	}
	if len(doc.Content) == 0 {
		return nil, nil
	}
	return doc.Content[0], nil
}

// patchPlugin returns the plugin path in our block, if the text has one.
func patchPlugin(text string) (string, bool) {
	before, after, found, err := cutBlock(text)
	if !found || err != nil {
		return "", false
	}
	block := text[len(before) : len(text)-len(after)]
	var entries []struct {
		Insert []struct{ ID, Name string }
	}
	if yaml.Unmarshal([]byte(block), &entries) != nil {
		return "", false
	}
	for _, e := range entries {
		for _, row := range e.Insert {
			if row.ID == rowID {
				return row.Name, true
			}
		}
	}
	return "", false
}
