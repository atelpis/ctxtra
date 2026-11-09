//go:build unix

package cli

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"
	"testing"
	"time"

	"github.com/ctxtra/ctxtra/internal/mcp/upstream"
)

// mcp add and mcp show stop a server with every process it started.
func TestServerToolsStopsDescendants(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	pidfile := filepath.Join(dir, "child.pid")
	s := upstream.Server{ID: "a", Name: "a", Command: []string{exe, "-child", pidfile}, Env: map[string]string{"CTXTRA_TEST_MCPSERVER": "1"}}
	tools, err := serverTools(s, dir, false)
	if err != nil || len(tools) == 0 {
		t.Fatalf("%v %v", tools, err)
	}
	b, err := os.ReadFile(pidfile)
	if err != nil {
		t.Fatal(err)
	}
	pid, _ := strconv.Atoi(string(b))
	for end := time.Now().Add(4 * time.Second); time.Now().Before(end); time.Sleep(50 * time.Millisecond) {
		if syscall.Kill(pid, 0) != nil {
			return
		}
	}
	_ = syscall.Kill(pid, syscall.SIGKILL)
	t.Errorf("the server's child %d still runs", pid)
}

// When the agent stops reading the proxy's output, the proxy doesn't die
// of SIGPIPE before it stopped its servers.
func TestProxyBrokenStdout(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	h := t.TempDir()
	pidfile := filepath.Join(h, "a.pid")
	cfg := filepath.Join(h, "config.yaml")
	if err := os.WriteFile(cfg, []byte("schema: ctxtra/v1\nmcp-servers:\n  a: {command: ['"+exe+"', -pidfile, '"+pidfile+"', -ignore-term], env: {CTXTRA_TEST_MCPSERVER: '1'}}\n"+
		"bindings:\n  - {name: all, match: {global: true}, mcp-servers: [a]}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(exe, "mcp", "proxy", "test")
	cmd.Dir = h
	cmd.Env = []string{"HOME=" + h, "PATH=" + os.Getenv("PATH"), "CTXTRA_TEST_HOOK=1", "CTXTRA_EXPERIMENTAL_MCP=1",
		"CTXTRA_CONFIG=" + cfg, "CTXTRA_LOG=" + filepath.Join(h, "log.jsonl")}
	in, _ := cmd.StdinPipe()
	out, _ := cmd.StdoutPipe()
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	for _, line := range []string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"t","version":"1"}}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`,
	} {
		_, _ = in.Write([]byte(line + "\n"))
	}
	dec := json.NewDecoder(out)
	for {
		var m map[string]any
		if err := dec.Decode(&m); err != nil {
			t.Fatal(err)
		}
		if m["id"] == float64(2) {
			break
		}
	}
	b, _ := os.ReadFile(pidfile)
	pid, _ := strconv.Atoi(string(b))
	_ = out.Close() // the agent stops reading
	_, _ = in.Write([]byte(`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"a__big","arguments":{}}}` + "\n"))
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case <-done:
	case <-time.After(15 * time.Second):
		_ = cmd.Process.Kill()
		t.Fatal("the proxy didn't exit")
	}
	for end := time.Now().Add(4 * time.Second); time.Now().Before(end); time.Sleep(50 * time.Millisecond) {
		if syscall.Kill(pid, 0) != nil {
			return
		}
	}
	_ = syscall.Kill(pid, syscall.SIGKILL)
	t.Errorf("the server %d still runs", pid)
}
