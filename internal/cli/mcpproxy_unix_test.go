//go:build unix

package cli

import (
	"os"
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
