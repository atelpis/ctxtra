package codex

import (
	"bytes"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"time"

	"github.com/BurntSushi/toml"

	"github.com/ctxtra/ctxtra/internal/agent"
	"github.com/ctxtra/ctxtra/internal/experimental"
	"github.com/ctxtra/ctxtra/internal/fsutil"
	"github.com/ctxtra/ctxtra/internal/paths"
)

// ctxtra's MCP entry is a block of lines between two markers in Codex's
// config.toml, so that setup finds and replaces its own lines without
// rewriting the file. Setup owns only the keys in mcpKeys: a key the user
// adds to the table, inside the block or after it, and subtables such as
// [mcp_servers.ctxtra.tools.NAME], stay.
const (
	beginMarker = "# >>> ctxtra MCP server: written by ctxtra setup; keys you add are kept"
	endMarker   = "# <<< ctxtra MCP server"
	tableHeader = "[mcp_servers.ctxtra]"
)

// StartupTimeout is the entry's startup_timeout_sec: above the proxy's own
// deadline for its first tool list, so that Codex never gives up on it.
const StartupTimeout = 30

// mcpKeys are the keys setup owns, in the order it writes them. required:
// Codex waits for a required server's first tool list (up to
// startup_timeout_sec), for an optional one about 1 second.
var mcpKeys = []string{"command", "args", "env_vars", "startup_timeout_sec", "required"}

func (Agent) MCPPath() string { return filepath.Join(dir(), "config.toml") }

// EnvVars are the variables Codex passes to the proxy: Codex starts MCP
// servers with an allow-listed environment. They make the proxy read the
// hook's config and log, reach the keychain on Linux, work behind a proxy,
// and give local servers their env-pass variables.
func EnvVars(envPass []string) []string {
	// The Secret Service's variables matter only on Linux; listed
	// everywhere, so the entry is the same on every system.
	vars := []string{"CTXTRA_CONFIG", "CTXTRA_LOG", experimental.MCPVar, "DBUS_SESSION_BUS_ADDRESS", "XDG_RUNTIME_DIR"}
	for _, v := range []string{"HTTPS_PROXY", "HTTP_PROXY", "NO_PROXY", "ALL_PROXY"} {
		vars = append(vars, v, strings.ToLower(v))
	}
	vars = append(vars, "SSL_CERT_FILE", "SSL_CERT_DIR")
	extra := slices.Clone(envPass)
	slices.Sort(extra)
	for _, v := range slices.Compact(extra) {
		if !slices.Contains(vars, v) {
			vars = append(vars, v)
		}
	}
	return vars
}

func (a Agent) wanted(bin string, envPass []string) map[string]any {
	return map[string]any{
		"command":             bin,
		"args":                toAny(agent.MCPArgs(a.Name())),
		"env_vars":            toAny(EnvVars(envPass)),
		"startup_timeout_sec": int64(StartupTimeout),
		"required":            true,
	}
}

func toAny(s []string) []any {
	out := make([]any, len(s))
	for i, v := range s {
		out[i] = v
	}
	return out
}

// tomlFile is config.toml as text, and as data.
type tomlFile struct {
	path       string
	lines      []string
	data       map[string]any
	begin, end int // the markers' lines; -1 without them
}

var errHandEdited = errors.New("the ctxtra MCP server isn't the one ctxtra setup wrote")

func readTOML(path string) (*tomlFile, error) {
	b, err := fsutil.ReadBounded(path, agent.MaxConfig)
	if errors.Is(err, os.ErrNotExist) {
		b, err = nil, nil
	}
	if err != nil {
		return nil, err
	}
	f := &tomlFile{path: path, data: map[string]any{}, begin: -1, end: -1}
	if _, err := toml.Decode(string(b), &f.data); err != nil {
		return nil, fmt.Errorf("not valid TOML: %v", err)
	}
	if len(b) > 0 {
		f.lines = strings.Split(strings.TrimSuffix(string(b), "\n"), "\n")
	}
	var begins, ends []int
	for i, l := range f.lines {
		switch strings.TrimSpace(l) {
		case beginMarker:
			begins = append(begins, i)
		case endMarker:
			ends = append(ends, i)
		}
	}
	switch {
	case len(begins) == 0 && len(ends) == 0:
	case len(begins) != 1 || len(ends) != 1 || ends[0] < begins[0]:
		return f, errHandEdited
	default:
		f.begin, f.end = begins[0], ends[0]
		if first := f.firstLine(); first < 0 || strings.TrimSpace(f.lines[first]) != tableHeader {
			return f, errHandEdited
		}
	}
	if f.begin < 0 && f.entry() != nil {
		return f, errHandEdited
	}
	return f, nil
}

// firstLine is the first line in the block that isn't blank or a comment.
func (f *tomlFile) firstLine() int {
	for i := f.begin + 1; i < f.end; i++ {
		if l := strings.TrimSpace(f.lines[i]); l != "" && !strings.HasPrefix(l, "#") {
			return i
		}
	}
	return -1
}

// entry is mcp_servers.ctxtra as data, nil when there is none.
func (f *tomlFile) entry() map[string]any {
	servers, _ := f.data["mcp_servers"].(map[string]any)
	e, ok := servers[agent.MCPEntryName]
	if !ok {
		return nil
	}
	m, _ := e.(map[string]any)
	if m == nil {
		return map[string]any{}
	}
	return m
}

func (f *tomlFile) text() string {
	if len(f.lines) == 0 {
		return ""
	}
	return strings.Join(f.lines, "\n") + "\n"
}

// keyLine is one key's line, as the TOML encoder writes it.
func keyLine(k string, v any) string {
	var b bytes.Buffer
	_ = toml.NewEncoder(&b).Encode(map[string]any{k: v})
	return strings.TrimSuffix(b.String(), "\n")
}

// lineKey is the key a line in the block sets, "" for none.
func lineKey(l string) string {
	k, _, ok := strings.Cut(l, "=")
	if !ok || strings.HasPrefix(strings.TrimSpace(l), "#") {
		return ""
	}
	return strings.Trim(strings.TrimSpace(k), `"'`)
}

func (a Agent) SetupMCP(o agent.MCPOptions, r *agent.Report) error {
	if !o.Remove && !o.Want {
		return nil
	}
	path := a.MCPPath()
	f, err := readTOML(path)
	again := "ctxtra setup"
	if o.Remove {
		again += " --remove"
	}
	switch {
	case errors.Is(err, errHandEdited):
		return fmt.Errorf("%s: %v; left alone. Restore or delete it, then run %s --agent codex", path, err, again)
	case err != nil:
		return fmt.Errorf("%s: %v; left alone", path, err)
	case o.Remove && f.begin < 0:
		return nil
	}
	expect := deepCopy(f.data)
	if o.Remove {
		f.lines = slices.Delete(f.lines, f.begin, f.end+1)
		if f.begin > 0 && f.begin < len(f.lines)+1 && strings.TrimSpace(f.lines[f.begin-1]) == "" &&
			(f.begin == len(f.lines) || strings.TrimSpace(f.lines[f.begin]) == "") {
			f.lines = slices.Delete(f.lines, f.begin-1, f.begin)
		}
		servers, _ := expect["mcp_servers"].(map[string]any)
		delete(servers, agent.MCPEntryName)
		if len(servers) == 0 {
			delete(expect, "mcp_servers")
		}
	} else {
		want := a.wanted(o.Binary, o.EnvPass)
		if f.begin < 0 {
			block := []string{beginMarker, tableHeader}
			for _, k := range mcpKeys {
				block = append(block, keyLine(k, want[k]))
			}
			block = append(block, endMarker)
			if len(f.lines) > 0 && strings.TrimSpace(f.lines[len(f.lines)-1]) != "" {
				f.lines = append(f.lines, "")
			}
			f.lines = append(f.lines, block...)
		} else {
			f.updateBlock(want)
		}
		servers, _ := expect["mcp_servers"].(map[string]any)
		if servers == nil {
			servers = map[string]any{}
			expect["mcp_servers"] = servers
		}
		e, _ := servers[agent.MCPEntryName].(map[string]any)
		if e == nil {
			e = map[string]any{}
		}
		maps.Copy(e, want)
		servers[agent.MCPEntryName] = e
	}
	out := f.text()
	// Codex refuses an invalid config, which would break every session:
	// the new file must say exactly what the old one said, but for ctxtra's
	// own keys.
	got := map[string]any{}
	if _, err := toml.Decode(out, &got); err != nil || !reflect.DeepEqual(normalize(got), normalize(expect)) {
		return fmt.Errorf("%s: %v; left alone. Restore or delete it, then run %s --agent codex", path, errHandEdited, again)
	}
	written, changed, err := fsutil.WriteAtomic(path, []byte(out), paths.BackupDir(), o.DryRun)
	if changed {
		r.Changes = append(r.Changes, written)
	}
	return err
}

// updateBlock sets the owned keys in the block: their lines are replaced
// in place, missing ones go after the table header.
func (f *tomlFile) updateBlock(want map[string]any) {
	header := f.firstLine()
	seen := map[string]bool{}
	for i := header + 1; i < f.end; i++ {
		k := lineKey(f.lines[i])
		if v, ok := want[k]; ok && !seen[k] {
			f.lines[i] = keyLine(k, v)
			seen[k] = true
		}
	}
	var missing []string
	for _, k := range mcpKeys {
		if !seen[k] {
			missing = append(missing, keyLine(k, want[k]))
		}
	}
	f.lines = slices.Insert(f.lines, header+1, missing...)
	f.end += len(missing)
}

func deepCopy(m map[string]any) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		if sub, ok := v.(map[string]any); ok {
			v = deepCopy(sub)
		}
		out[k] = v
	}
	return out
}

// normalize makes decoded data comparable: empty tables and missing ones
// are the same for this check.
func normalize(v any) any {
	switch x := v.(type) {
	case map[string]any:
		out := map[string]any{}
		for k, val := range x {
			out[k] = normalize(val)
		}
		return out
	case []map[string]any:
		out := make([]any, len(x))
		for i, val := range x {
			out[i] = normalize(val)
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, val := range x {
			out[i] = normalize(val)
		}
		return out
	}
	return v
}

// mcpBinary is the program of a well-formed entry.
func mcpBinary(e map[string]any) (bin string, ours bool) {
	bin, _ = e["command"].(string)
	args, _ := e["args"].([]any)
	return bin, len(args) >= 2 && args[0] == "mcp" && args[1] == "proxy"
}

func (a Agent) MCPStatus(bin string, o agent.MCPCheck) agent.Status {
	notices := a.CheckMCP(bin, o)
	f, err := readTOML(a.MCPPath())
	if err != nil || f.begin < 0 {
		return agent.Status{State: "no MCP entry", OK: len(notices) == 0, Details: notices}
	}
	b, _ := mcpBinary(f.entry())
	s := agent.Status{State: "MCP entry runs " + paths.Tilde(b), OK: len(notices) == 0, Details: notices}
	if _, ok := agent.CheckBinary(b); ok {
		s.State += " (ok)"
	}
	return s
}

func (a Agent) CheckMCP(bin string, o agent.MCPCheck) []string {
	path := a.MCPPath()
	repair := agent.Repair(a.Name())
	f, err := readTOML(path)
	switch {
	case !o.Want && !o.Known && (err != nil || f.begin < 0):
		return nil
	case errors.Is(err, errHandEdited):
		return []string{fmt.Sprintf("%s: %v; restore or delete it, then %s", paths.Tilde(path), err, repair)}
	case err != nil:
		return []string{agent.UnreadableNotice(path, err)}
	case f.begin < 0 && o.Known:
		return []string{agent.GoneNotice("the ctxtra MCP server is", path, a.Name(), repair)}
	case f.begin < 0:
		return []string{"the ctxtra MCP server is missing; " + repair}
	}
	b, ours := mcpBinary(f.entry())
	if !ours {
		return []string{fmt.Sprintf("%s: %v; restore or delete it, then %s", paths.Tilde(path), errHandEdited, repair)}
	}
	out := agent.BinaryNotices("the ctxtra MCP server runs", []string{b}, bin, repair)
	if t, _ := f.entry()["startup_timeout_sec"].(int64); t < StartupTimeout {
		out = append(out, "the ctxtra MCP server has a shorter startup_timeout_sec than setup writes; "+repair)
	}
	if r, _ := f.entry()["required"].(bool); !r {
		out = append(out, "the ctxtra MCP server isn't required, so Codex doesn't wait for its tools; "+repair)
	}
	return out
}

func (a Agent) MCP() agent.MCPHost { return a }

func (a Agent) MCPCommand() ([]string, bool) {
	f, err := readTOML(a.MCPPath())
	if err != nil || f.begin < 0 {
		return nil, false
	}
	e := f.entry()
	bin, ours := mcpBinary(e)
	if !ours {
		return nil, false
	}
	out := []string{bin}
	args, _ := e["args"].([]any)
	for _, x := range args {
		s, _ := x.(string)
		out = append(out, s)
	}
	return out, true
}

// MCPDeadline: Codex waits about 1 second for an optional server's first
// tool list (measured with Codex 0.166.0); the entry is required, so Codex
// waits for it up to startup_timeout_sec.
func (Agent) MCPDeadline() time.Duration { return agent.MCPDeadline }
