// Package names gives MCP tools the names sessions see: <server>__<tool>,
// in characters and a length that every supported agent accepts and can
// call. The same tool gets the same name in every session, agent and
// machine, so the agents' permission rules keep working.
package names

import (
	"cmp"
	"crypto/sha256"
	"encoding/hex"
	"slices"
	"strings"
)

// MaxLen is the longest name. Muse shortens tool names over about 40
// characters and then can't call them; the strictest model APIs allow 64,
// minus the mcp__ctxtra__ prefix that Claude Code and Pi add (51).
const MaxLen = 38

// Tool is a tool of an upstream server, by its server's name and its own.
type Tool struct{ Server, Name string }

// Name is a tool and the name sessions see.
type Name struct {
	Tool
	Shown     string
	Shortened bool // Shown is not Server__Name
}

// hash is the hex SHA-256 of a tool; a variable so tests can make
// collisions.
var hash = func(t Tool) string {
	sum := sha256.Sum256([]byte(t.Server + "\x00" + t.Name))
	return hex.EncodeToString(sum[:])
}

// Assign names tools, which may come in any order. Names that end up
// equal get longer hashes, in steps; a tool still equal to another after
// the last step is left out and returned in dropped. Names are sorted by
// server, then tool.
func Assign(tools []Tool) (named []Name, dropped []Tool) {
	tools = slices.Clone(tools)
	slices.SortFunc(tools, func(a, b Tool) int { return cmp.Or(cmp.Compare(a.Server, b.Server), cmp.Compare(a.Name, b.Name)) })
	tools = slices.Compact(tools)
	level := make([]int, len(tools))
	for {
		named = named[:0]
		groups := map[string][]int{}
		for i, t := range tools {
			n := name(t, level[i])
			named = append(named, n)
			groups[n.Shown] = append(groups[n.Shown], i)
		}
		changed := false
		for _, g := range groups {
			if len(g) < 2 {
				continue
			}
			for _, i := range g {
				if named[i].Shortened && level[i] < len(steps(tools[i].Server))-1 {
					level[i]++
					changed = true
				}
			}
		}
		if !changed {
			break
		}
	}
	// What still collides: the first of each name, in sorted order, keeps
	// it; an unshortened name always does.
	owner := map[string]int{}
	for i, n := range named {
		if j, ok := owner[n.Shown]; !ok || named[j].Shortened && !n.Shortened {
			owner[n.Shown] = i
		}
	}
	var out []Name
	for i, n := range named {
		if owner[n.Shown] == i {
			out = append(out, n)
		} else {
			dropped = append(dropped, n.Tool)
		}
	}
	return out, dropped
}

// steps are the hash lengths for a server: 6, then 10, then as many as fit
// while server__ stays whole, at most 16.
func steps(server string) []int { return []int{6, 10, min(16, MaxLen-3-len(server))} }

// name is t's name with the hash length of step level.
func name(t Tool, level int) Name {
	tool := sanitize(t.Name)
	full := t.Server + "__" + tool
	if tool == t.Name && len(full) <= MaxLen {
		return Name{Tool: t, Shown: full}
	}
	n := steps(t.Server)[level]
	h := hash(t)[:n]
	if len(full)+1+n > MaxLen {
		full = full[:MaxLen-1-n]
	}
	return Name{Tool: t, Shown: full + "_" + h, Shortened: true}
}

// sanitize replaces every character outside A-Z, a-z, 0-9 and _ with _.
func sanitize(s string) string {
	return strings.Map(func(r rune) rune {
		if r == '_' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' {
			return r
		}
		return '_'
	}, s)
}

// Describe is the description a session sees: a shortened tool's starts
// with its server and its own name.
func Describe(n Name, description string) string {
	if !n.Shortened {
		return description
	}
	prefix := "[" + n.Server + ": " + n.Name + "]"
	if description == "" {
		return prefix
	}
	return prefix + " " + description
}
