// Command probe is a test tool for the MCP proxy experiment.
//
//	probe mcp -log FILE [-instructions TEXT]   a stdio MCP server that records what the agent gives it
//	probe hook -log FILE -real BIN [args]   runs BIN with args, after recording the hook's process chain and input
//
// Both append one JSON record per line to FILE. Environment values are
// recorded only for names that look like folders or sessions, never for
// names that look like keys, tokens or secrets.
package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: probe mcp|hook ...")
		os.Exit(2)
	}
	switch os.Args[1] {
	case "mcp":
		serveMCP(flagValue(os.Args[2:], "-log"))
	case "hook":
		runHook(os.Args[2:])
	default:
		os.Exit(2)
	}
}

func flagValue(args []string, name string) string {
	for i, a := range args {
		if a == name && i+1 < len(args) {
			return args[i+1]
		}
	}
	return ""
}

var logMu sync.Mutex

func record(path string, rec map[string]any) {
	rec["time"] = time.Now().Format(time.RFC3339Nano)
	rec["pid"] = os.Getpid()
	b, _ := json.Marshal(rec)
	logMu.Lock()
	defer logMu.Unlock()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = f.Write(append(b, '\n'))
}

var (
	interesting = regexp.MustCompile(`DIR|PWD|PROJECT|WORKSPACE|ROOT|CWD|SESSION`)
	secret      = regexp.MustCompile(`KEY|TOKEN|SECRET|PASS|AUTH|CRED|COOKIE`)
)

// environment is every variable's name, and the values of the interesting ones.
func environment() (names []string, values map[string]string) {
	values = map[string]string{}
	for _, kv := range os.Environ() {
		k, v, _ := strings.Cut(kv, "=")
		names = append(names, k)
		if interesting.MatchString(strings.ToUpper(k)) && !secret.MatchString(strings.ToUpper(k)) {
			values[k] = v
		}
	}
	sort.Strings(names)
	return names, values
}

type proc struct {
	PID     int    `json:"pid"`
	Command string `json:"command"`
}

// ancestors is the parent chain from the parent process up to pid 1.
func ancestors() []proc {
	var out []proc
	pid := os.Getppid()
	for i := 0; pid > 1 && i < 30; i++ {
		b, err := exec.Command("ps", "-o", "ppid=,command=", "-p", strconv.Itoa(pid)).Output()
		if err != nil {
			break
		}
		line := strings.TrimSpace(string(b))
		ppidStr, cmd, _ := strings.Cut(line, " ")
		if len(cmd) > 300 {
			cmd = cmd[:300] + "…"
		}
		out = append(out, proc{pid, strings.TrimSpace(cmd)})
		pid, _ = strconv.Atoi(strings.TrimSpace(ppidStr))
	}
	return out
}

func startRecord(event string) map[string]any {
	wd, err := os.Getwd()
	if err != nil {
		wd = "error: " + err.Error()
	}
	names, values := environment()
	return map[string]any{"event": event, "cwd": wd, "ppid": os.Getppid(), "ancestors": ancestors(),
		"env_names": names, "env": values, "args": os.Args[1:]}
}

// runHook records the hook's process chain and the folder fields of its
// input, then runs the real ctxtra with the same input.
func runHook(args []string) {
	logPath, real := flagValue(args, "-log"), flagValue(args, "-real")
	var rest []string
	for i := 0; i < len(args); i++ {
		if args[i] == "-log" || args[i] == "-real" {
			i++
			continue
		}
		rest = append(rest, args[i])
	}
	in, _ := readAll(os.Stdin)
	rec := startRecord("hook")
	var input map[string]any
	if json.Unmarshal(in, &input) == nil {
		fields := map[string]any{}
		for _, k := range []string{"cwd", "session_id", "sessionId", "hook_event_name", "source", "workspace_roots",
			"workspaceRoots", "workspacePaths", "ctxtra_plugin", "conversation_id", "generation_id"} {
			if v, ok := input[k]; ok {
				fields[k] = v
			}
		}
		var keys []string
		for k := range input {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		rec["input_keys"] = keys
		rec["input"] = fields
	} else {
		rec["input_raw_len"] = len(in)
	}
	record(logPath, rec)
	cmd := exec.Command(real, rest...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = bytes.NewReader(in), os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			os.Exit(ee.ExitCode())
		}
		os.Exit(1)
	}
}

func readAll(f *os.File) ([]byte, error) {
	var b bytes.Buffer
	_, err := b.ReadFrom(f)
	return b.Bytes(), err
}

// Tool names to test the agents' naming rules. Lengths are counted before
// any prefix the agent adds; each long name starts with a unique marker.
func toolNames() []string {
	names := []string{"probe_ping", "linear-work__list_issues", "dot.name.tool", "colon:name:tool"}
	for _, n := range []int{40, 60, 64, 65, 80, 120} {
		m := fmt.Sprintf("L%03d_", n)
		names = append(names, m+strings.Repeat("x", n-len(m)))
	}
	return names
}

// lateTool is offered only after notifications/tools/list_changed.
const lateTool = "late_added_tool"

type server struct {
	log      string
	out      *json.Encoder
	outMu    sync.Mutex
	nextID   int
	pending  map[string]string // our request ID -> method
	late     bool
	notified bool
	listed   bool
	mu       sync.Mutex
}

func (s *server) send(v any) {
	s.outMu.Lock()
	defer s.outMu.Unlock()
	s.out.Encode(v)
}

func (s *server) request(method string, params any) {
	s.mu.Lock()
	s.nextID++
	id := fmt.Sprintf("probe-%d", s.nextID)
	s.pending[id] = method
	s.mu.Unlock()
	msg := map[string]any{"jsonrpc": "2.0", "id": id, "method": method}
	if params != nil {
		msg["params"] = params
	}
	record(s.log, map[string]any{"event": "server_request", "method": method, "id": id})
	s.send(msg)
}

func serveMCP(logPath string) {
	s := &server{log: logPath, out: json.NewEncoder(os.Stdout), pending: map[string]string{}}
	record(logPath, startRecord("start"))
	var clientCaps map[string]any
	sc := bufio.NewScanner(os.Stdin)
	sc.Buffer(nil, 16<<20)
	for sc.Scan() {
		var msg map[string]any
		if err := json.Unmarshal(sc.Bytes(), &msg); err != nil {
			record(logPath, map[string]any{"event": "bad_line", "error": err.Error()})
			continue
		}
		id, hasID := msg["id"]
		method, _ := msg["method"].(string)
		if method == "" { // a response to our request
			idStr := fmt.Sprint(id)
			s.mu.Lock()
			m := s.pending[idStr]
			s.mu.Unlock()
			record(logPath, map[string]any{"event": "client_response", "for": m, "id": idStr, "result": msg["result"], "error": msg["error"]})
			continue
		}
		params, _ := msg["params"].(map[string]any)
		rec := map[string]any{"event": "client_message", "method": method, "id": id}
		if method == "initialize" {
			rec["params"] = params
			wd, _ := os.Getwd()
			rec["cwd"] = wd
		}
		if method == "tools/call" {
			rec["params"] = params
		}
		record(logPath, rec)
		reply := func(result any) { s.send(map[string]any{"jsonrpc": "2.0", "id": id, "result": result}) }
		switch method {
		case "initialize":
			clientCaps, _ = params["capabilities"].(map[string]any)
			pv, _ := params["protocolVersion"].(string)
			if pv == "" {
				pv = "2025-06-18"
			}
			result := map[string]any{"protocolVersion": pv,
				"capabilities": map[string]any{"tools": map[string]any{"listChanged": true}},
				"serverInfo":   map[string]any{"name": "probe", "version": "0.0.1"}}
			// -instructions TEXT: the server's instructions, to learn
			// which agents show them to the model.
			if text := flagValue(os.Args[2:], "-instructions"); text != "" {
				result["instructions"] = text
			}
			reply(result)
		case "notifications/initialized":
			if _, ok := clientCaps["roots"]; ok {
				go s.request("roots/list", nil)
			}
		case "notifications/roots/list_changed":
			go s.request("roots/list", nil)
		case "ping":
			reply(map[string]any{})
		case "tools/list":
			var tools []any
			names := toolNames()
			if only := flagValue(os.Args[2:], "-names"); only != "" {
				names = strings.Split(only, ",")
			}
			s.mu.Lock()
			if s.late {
				names = append(names, lateTool)
			}
			s.mu.Unlock()
			for _, n := range names {
				tools = append(tools, map[string]any{"name": n, "description": "Probe tool " + n,
					"inputSchema": map[string]any{"type": "object",
						"properties": map[string]any{"note": map[string]any{"type": "string"}}}})
			}
			rec := map[string]any{"event": "tools_listed", "count": len(tools), "late": s.late}
			// -list-delay D answers the first tool list after D, the way
			// a proxy that waits for slow servers does.
			if d, _ := time.ParseDuration(flagValue(os.Args[2:], "-list-delay")); d > 0 && !s.listed {
				s.listed = true
				go func() {
					time.Sleep(d)
					rec["delay"] = d.String()
					record(logPath, rec)
					reply(map[string]any{"tools": tools})
				}()
				continue
			}
			s.listed = true
			record(logPath, rec)
			reply(map[string]any{"tools": tools})
		case "tools/call":
			name, _ := params["name"].(string)
			reply(map[string]any{"content": []any{map[string]any{"type": "text", "text": "PROBE-RESULT name=" + name}}})
			s.mu.Lock()
			first := !s.notified
			s.notified = true
			s.late = true
			s.mu.Unlock()
			if first {
				record(logPath, map[string]any{"event": "sent_list_changed"})
				s.send(map[string]any{"jsonrpc": "2.0", "method": "notifications/tools/list_changed"})
			}
		case "resources/list":
			reply(map[string]any{"resources": []any{}})
		case "resources/templates/list":
			reply(map[string]any{"resourceTemplates": []any{}})
		case "prompts/list":
			reply(map[string]any{"prompts": []any{}})
		default:
			if hasID {
				s.send(map[string]any{"jsonrpc": "2.0", "id": id, "error": map[string]any{"code": -32601, "message": "method not found"}})
			}
		}
	}
	record(logPath, map[string]any{"event": "stdin_closed"})
}
