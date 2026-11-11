package e2e

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"filippo.io/age"

	"github.com/ctxtra/ctxtra/e2e/driver"
	"github.com/ctxtra/ctxtra/e2e/mock"
)

// fileKey gives the session's credential store a key in a file, so that
// nothing in the test reads or writes the user's OS keychain, which is the
// same for every home folder.
func fileKey(t *testing.T, s *Session) {
	t.Helper()
	id, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	dir := s.Path(".ctxtra/mcp")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	for name, data := range map[string]string{"key": id.String() + "\n", "key.json": `{"version":1,"location":"file"}` + "\n"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

// apiServer is a remote MCP server on 127.0.0.1 that wants an API key in
// X-Api-Key, with one tool, web_ping. It records the calls it answers.
type apiServer struct {
	*httptest.Server
	key   string
	mu    sync.Mutex
	calls []string
}

func newAPIServer(t *testing.T, key string) *apiServer {
	a := &apiServer{key: key}
	a.Server = httptest.NewServer(http.HandlerFunc(a.serve))
	t.Cleanup(a.Close)
	return a
}

func (a *apiServer) serve(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("X-Api-Key") != a.key {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		ID     json.RawMessage `json:"id"`
		Method string          `json:"method"`
		Params struct {
			ProtocolVersion string `json:"protocolVersion"`
			Name            string `json:"name"`
		} `json:"params"`
	}
	if json.NewDecoder(r.Body).Decode(&req) != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	if req.ID == nil { // a notification
		w.WriteHeader(http.StatusAccepted)
		return
	}
	var result any
	var rpcErr any
	switch req.Method {
	case "initialize":
		v := req.Params.ProtocolVersion
		if v == "" || v > "2025-11-25" {
			v = "2025-06-18"
		}
		result = map[string]any{"protocolVersion": v, "capabilities": map[string]any{"tools": map[string]any{}},
			"serverInfo": map[string]any{"name": "web", "version": "1"}}
	case "tools/list":
		result = map[string]any{"tools": []any{map[string]any{"name": "web_ping", "description": "Ping.",
			"inputSchema": map[string]any{"type": "object"}}}}
	case "tools/call":
		a.mu.Lock()
		a.calls = append(a.calls, req.Params.Name)
		a.mu.Unlock()
		result = map[string]any{"content": []any{map[string]any{"type": "text", "text": "PONG"}}}
	default:
		rpcErr = map[string]any{"code": -32601, "message": "method not found"}
	}
	w.Header().Set("Content-Type", "application/json")
	resp := map[string]any{"jsonrpc": "2.0", "id": req.ID}
	if rpcErr != nil {
		resp["error"] = rpcErr
	} else {
		resp["result"] = result
	}
	_ = json.NewEncoder(w).Encode(resp)
}

func (a *apiServer) called() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]string(nil), a.calls...)
}

// A remote server on 127.0.0.1 with a secret value in the credential
// store, in each agent: the proxy reaches the network and the key from
// inside the agent.
func TestMCPRemoteCredential(t *testing.T) {
	forEachAgent(t, nil, func(t *testing.T, a Agent) {
		const key = "e2e-api-key"
		web := newAPIServer(t, key)
		s := mcpSession(t, a, func(s *Session) string {
			fileKey(t, s)
			return `schema: ctxtra/v1
mcp-servers:
  web: {url: '` + web.URL + `/mcp', secret-headers: [X-Api-Key]}
bindings:
  - name: a
    match: {dirs: [~/dev/a]}
    mcp-servers: [web]
`
		})
		s.Write("dev/a/README.md", "x")
		login := exec.Command(s.Bin, "mcp", "login", "web")
		login.Dir, login.Env, login.Stdin = s.Home, s.Env(), strings.NewReader(key+"\n")
		if out, err := login.CombinedOutput(); err != nil || !strings.Contains(string(out), "stored its secret values") {
			t.Fatalf("mcp login: %v\n%s", err, out)
		}
		if strings.Contains(readFile(t, s.Path("ctx/ctxtra.yaml")), key) {
			t.Fatal("the key is in the config")
		}
		s.Run(s.Path("dev/a"), driver.Options{}, mcpScript(a.Name(), "web__web_ping")...)
		if got := web.called(); len(got) != 1 || got[0] != "web_ping" {
			t.Errorf("the server got calls %v", got)
		}
	})
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// A server that is missing at the start because its secret value isn't
// set gets its tools when the user sets it during the session: here the
// session's own command runs ctxtra mcp login, then waits.
func TestMCPSignInDuringSession(t *testing.T) {
	forEachAgent(t, nil, func(t *testing.T, a Agent) {
		if a.Name() != "claude" {
			t.Skip("checked in Claude Code, which takes tool list changes")
		}
		const key = "e2e-api-key"
		web := newAPIServer(t, key)
		s := mcpSession(t, a, func(s *Session) string {
			fileKey(t, s)
			return `schema: ctxtra/v1
mcp-servers:
  web: {url: '` + web.URL + `/mcp', secret-headers: [X-Api-Key]}
bindings:
  - name: a
    match: {dirs: [~/dev/a]}
    mcp-servers: [web]
`
		})
		s.Write("dev/a/README.md", "x")
		s.Write("key.txt", key+"\n")
		adjust := s.Adjust
		s.Adjust = func(cmd *exec.Cmd) {
			adjust(cmd)
			for i, arg := range cmd.Args {
				if arg == "--allowedTools" {
					cmd.Args[i+1] += ",Bash"
				}
			}
		}
		login := fmt.Sprintf("'%s' mcp login web < '%s' && sleep 5", s.Bin, s.Path("key.txt"))
		r := s.Run(s.Path("dev/a"), driver.Options{},
			mock.Call("Bash", map[string]any{"command": login, "description": "sign in"}),
			mcpCalls(a.Name(), "web__web_ping"))
		for _, tool := range mock.ToolNames(r.Requests[0].API, r.Requests[0].Body) {
			if strings.Contains(tool.Name, "web__web_ping") {
				t.Fatalf("the first request already has %s: no sign-in during the session was checked", tool.Name)
			}
		}
		if got := web.called(); len(got) != 1 {
			t.Errorf("the server got calls %v", got)
		}
	})
}
