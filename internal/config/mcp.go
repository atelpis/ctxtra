package config

import (
	"fmt"
	"net"
	"net/url"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/ctxtra/ctxtra/internal/experimental"
	"github.com/ctxtra/ctxtra/internal/mcp/upstream"
	"github.com/ctxtra/ctxtra/internal/paths"
)

// The format of mcp-servers is provisional: how servers are declared and
// bound to folders is not decided yet, so it works only with
// experimental.MCP.

// MaxServerName is the longest server name: it leaves room for tool names
// in the 38 characters that every agent accepts.
const MaxServerName = 20

// MCPServer returns the valid server called name.
func (c *Config) MCPServer(name string) (upstream.Server, bool) {
	if c.mcpBroken[name] {
		return upstream.Server{}, false
	}
	i := slices.IndexFunc(c.MCPServers, func(s upstream.Server) bool { return s.Name == name })
	if i < 0 {
		return upstream.Server{}, false
	}
	return c.MCPServers[i], true
}

// CheckServerName says what is wrong with a server name, nil when nothing.
func CheckServerName(name string) error {
	if len(name) <= MaxServerName && reServer.MatchString(name) && !strings.Contains(name, "__") && !strings.HasSuffix(name, "_") {
		return nil
	}
	msg := fmt.Sprintf("server name %q: names are 1 to %d characters of a-z, 0-9 and _, start with a letter, and have no __ and no _ at the end", name, MaxServerName)
	if s := suggestServerName(name); s != "" && s != name {
		msg += "; for example " + s
	}
	return fmt.Errorf("%s", msg)
}

var reServer = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

// suggestServerName is name made valid, or "" when nothing is left of it.
func suggestServerName(name string) string {
	s := regexp.MustCompile(`[^a-z0-9]+`).ReplaceAllString(strings.ToLower(name), "_")
	s = strings.TrimLeft(s, "_0123456789")
	if len(s) > MaxServerName {
		s = s[:MaxServerName]
	}
	return strings.TrimRight(s, "_")
}

var (
	reLine   = regexp.MustCompile(`^line (\d+): `)
	reVar    = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
	reHeader = regexp.MustCompile("^[!#$%&'*+.^_`|~0-9A-Za-z-]+$")
)

// decodeMCPServers sets c.MCPServers from the mcp-servers map. A broken
// server is skipped alone, with a problem: without it, sessions get fewer
// tools, never more.
func (c *Config) decodeMCPServers(n *yaml.Node) {
	c.mcpBroken = map[string]bool{}
	if n.Kind != yaml.MappingNode {
		c.problem(c.File, fmt.Errorf("line %d: mcp-servers must be a map of servers by name; no server is used", n.Line))
		return
	}
	seen := map[string]bool{}
	for i := 0; i+1 < len(n.Content); i += 2 {
		k, v := n.Content[i], n.Content[i+1]
		name := k.Value
		err := CheckServerName(name)
		if err == nil && seen[name] {
			// Neither declaration is used: which one is meant isn't clear.
			err = fmt.Errorf("server %q is there twice", name)
			c.MCPServers = slices.DeleteFunc(c.MCPServers, func(s upstream.Server) bool { return s.Name == name })
		}
		seen[name] = true
		var s upstream.Server
		if err == nil {
			s, err = c.decodeMCPServer(name, v)
		}
		if err != nil {
			c.mcpBroken[name] = true
			line, msg := k.Line, flat(err)
			if m := reLine.FindStringSubmatch(msg); m != nil {
				line, _ = strconv.Atoi(m[1])
				msg = msg[len(m[0]):]
			}
			c.problem(c.File, fmt.Errorf("line %d: mcp-servers.%s: %s; server skipped", line, name, msg))
			continue
		}
		c.MCPServers = append(c.MCPServers, s)
	}
}

var (
	localKeys  = []string{"env", "env-pass", "secret-env"}
	remoteKeys = []string{"headers", "secret-headers", "client-id", "scopes"}
)

func (c *Config) decodeMCPServer(name string, n *yaml.Node) (upstream.Server, error) {
	s := upstream.Server{ID: name, Name: name}
	f, err := fields(n, slices.Concat([]string{"url", "command", "notes"}, localKeys, remoteKeys)...)
	if err != nil {
		return s, err
	}
	switch {
	case f["url"] != nil && f["command"] != nil:
		return s, fmt.Errorf("line %d: a server has url or command, not both", n.Line)
	case f["url"] == nil && f["command"] == nil:
		return s, fmt.Errorf("line %d: a server needs url (a remote server) or command (a local one)", n.Line)
	}
	other := remoteKeys
	if f["url"] != nil {
		other = localKeys
		if s.URL, err = str(f["url"], "url"); err != nil {
			return s, err
		}
		if err := CheckServerURL(s.URL); err != nil {
			return s, fmt.Errorf("line %d: %w", f["url"].Line, err)
		}
	} else {
		if s.Command, err = strList(f["command"], "command"); err != nil {
			return s, err
		}
		if s.Command[0], err = CheckProgram(s.Command[0]); err != nil {
			return s, fmt.Errorf("line %d: %w", f["command"].Line, err)
		}
	}
	for _, k := range other {
		if v := f[k]; v != nil {
			kind := "local"
			if f["url"] != nil {
				kind = "remote"
			}
			return s, fmt.Errorf("line %d: %s doesn't apply to a %s server", v.Line, k, kind)
		}
	}
	if v := f["notes"]; v != nil {
		if s.Notes, err = str(v, "notes"); err != nil {
			return s, err
		}
	}
	if v := f["env"]; v != nil {
		if s.Env, err = strMap(v, "env", reVar); err != nil {
			return s, err
		}
	}
	if s.EnvPass, err = nameList(f["env-pass"], "env-pass", reVar); err != nil {
		return s, err
	}
	if s.SecretEnv, err = nameList(f["secret-env"], "secret-env", reVar); err != nil {
		return s, err
	}
	if v := f["headers"]; v != nil {
		if s.Headers, err = strMap(v, "headers", reHeader); err != nil {
			return s, err
		}
	}
	if s.SecretHeaders, err = nameList(f["secret-headers"], "secret-headers", reHeader); err != nil {
		return s, err
	}
	if v := f["client-id"]; v != nil {
		if s.ClientID, err = str(v, "client-id"); err != nil {
			return s, err
		}
	}
	if v := f["scopes"]; v != nil {
		if s.Scopes, err = strList(v, "scopes"); err != nil {
			return s, err
		}
	}
	if err := overlap(s.Env, s.EnvPass, s.SecretEnv, "variable"); err != nil {
		return s, err
	}
	if err := overlap(s.Headers, nil, s.SecretHeaders, "header"); err != nil {
		return s, err
	}
	c.secretLooking(name, s)
	return s, nil
}

// overlap reports a name that two of a server's keys set.
func overlap(m map[string]string, pass, secret []string, what string) error {
	seen := map[string]bool{}
	for k := range m {
		seen[strings.ToLower(k)] = true
	}
	for _, list := range [][]string{pass, secret} {
		for _, k := range list {
			if seen[strings.ToLower(k)] {
				return fmt.Errorf("the %s %s is set twice", what, k)
			}
			seen[strings.ToLower(k)] = true
		}
	}
	return nil
}

// CheckServerURL accepts https URLs, and http only on this machine.
func CheckServerURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.User != nil || u.Fragment != "" {
		return fmt.Errorf("url %q is not a valid server URL", raw)
	}
	switch u.Scheme {
	case "https":
		return nil
	case "http":
		if host := u.Hostname(); host == "localhost" || net.ParseIP(host) != nil && net.ParseIP(host).IsLoopback() {
			return nil
		}
	}
	return fmt.Errorf("url %q: remote servers need https (http only for 127.0.0.1, ::1 or localhost)", raw)
}

// CheckProgram returns a local server's program as the proxy starts it: an
// absolute path, ~ expanded, or a bare name looked up in PATH. A relative
// path is refused: local servers start in the session folder, where the
// repository could supply the program.
func CheckProgram(p string) (string, error) {
	if strings.HasPrefix(p, "~/") {
		p = paths.Expand(p)
	}
	switch {
	case filepath.IsAbs(p):
		return filepath.Clean(p), nil
	case !strings.Contains(p, "/") && p != "." && p != "..":
		return p, nil
	}
	return "", fmt.Errorf("command %q: the program must be an absolute path or a name in PATH, not a relative path", p)
}

func strMap(n *yaml.Node, what string, key *regexp.Regexp) (map[string]string, error) {
	if n.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("line %d: %s must be a map", n.Line, what)
	}
	out := map[string]string{}
	for i := 0; i+1 < len(n.Content); i += 2 {
		k, v := n.Content[i], n.Content[i+1]
		if !key.MatchString(k.Value) {
			return nil, fmt.Errorf("line %d: %s: %q is not a valid name", k.Line, what, k.Value)
		}
		if _, dup := out[k.Value]; dup {
			return nil, fmt.Errorf("line %d: %s: duplicate key %q", k.Line, what, k.Value)
		}
		if v.Kind != yaml.ScalarNode || v.ShortTag() == "!!null" {
			return nil, fmt.Errorf("line %d: %s.%s must be a value", v.Line, what, k.Value)
		}
		out[k.Value] = v.Value
	}
	return out, nil
}

// nameList is a list of variable or header names; nil for a missing key.
func nameList(n *yaml.Node, what string, re *regexp.Regexp) ([]string, error) {
	if n == nil {
		return nil, nil
	}
	list, err := strList(n, what)
	if err != nil {
		return nil, err
	}
	for i, s := range list {
		if !re.MatchString(s) {
			return nil, fmt.Errorf("line %d: %s: %q is not a valid name", n.Line, what, s)
		}
		if slices.Contains(list[:i], s) {
			return nil, fmt.Errorf("line %d: %s: %s is listed twice", n.Line, what, s)
		}
	}
	return list, nil
}

// secretLooking adds a note for each header or variable whose value looks
// like a secret, which belongs in the credential store.
func (c *Config) secretLooking(name string, s upstream.Server) {
	for _, h := range sortedKeys(s.Headers) {
		if SecretHeader(h) {
			c.Notes = append(c.Notes, fmt.Sprintf("mcp-servers.%s: headers.%s looks like a secret, which the config shouldn't hold; use secret-headers: [%s]", name, h, h))
		}
	}
	for _, v := range sortedKeys(s.Env) {
		if SecretVar(v) {
			c.Notes = append(c.Notes, fmt.Sprintf("mcp-servers.%s: env.%s looks like a secret, which the config shouldn't hold; use secret-env: [%s]", name, v, v))
		}
	}
}

// SecretHeader reports whether a header's value is likely a secret.
func SecretHeader(h string) bool {
	l := strings.ToLower(h)
	return l == "authorization" || l == "cookie" || strings.Contains(l, "token") || strings.Contains(l, "key")
}

// SecretVar reports whether a variable's value is likely a secret.
func SecretVar(v string) bool {
	u := strings.ToUpper(v)
	return strings.HasSuffix(u, "_TOKEN") || strings.Contains(u, "PASSWORD") || strings.Contains(u, "SECRET") || strings.HasSuffix(u, "_KEY")
}

func sortedKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}

// mcpNames resolves a binding's mcp-servers. A name that can't be used is
// skipped alone, with a problem: the binding's other entries still apply.
// in is the pinned source of the binding's file, or nil.
func (c *Config) mcpNames(n *yaml.Node, file string, in *Source) ([]string, error) {
	list, err := strList(n, "mcp-servers")
	if err != nil {
		return nil, err
	}
	var out []string
	for _, name := range list {
		var err error
		_, declared := c.MCPServer(name)
		switch {
		case CheckServerName(name) != nil:
			err = CheckServerName(name)
		case slices.Contains(out, name):
			err = fmt.Errorf("server %s is listed twice", name)
		case c.checking:
			// source check reads no user config: it can't know the names.
		case in != nil && (declared || c.mcpBroken[name]):
			err = fmt.Errorf("server %s: binding files of pinned sources can't turn on the config's servers", name)
		case c.mcpBroken[name]:
			err = fmt.Errorf("server %s has an error in mcp-servers", name)
		case !declared:
			err = fmt.Errorf("server %s is not in mcp-servers of %s", name, paths.Tilde(c.File))
		}
		if err != nil {
			c.problem(file, fmt.Errorf("line %d: %w; skipped", n.Line, err))
			continue
		}
		out = append(out, name)
	}
	return out, nil
}

// mcpOn reports whether MCP servers are on: everything about them is off
// until they are released.
func mcpOn() bool { return experimental.MCP() }
