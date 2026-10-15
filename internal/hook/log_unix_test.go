//go:build unix

package hook

import (
	"bytes"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/atelpis/ctxtra/internal/testenv"
)

// A FIFO as the log must not block the hook: the agent waits for its answer.
func TestLogFIFODoesNotBlock(t *testing.T) {
	h := testenv.Home(t)
	fifo := filepath.Join(h, "log.fifo")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Skip(err)
	}
	t.Setenv("CTXTRA_LOG", fifo)
	done := make(chan string, 1)
	go func() {
		var out bytes.Buffer
		Run("claude", "SessionStart", strings.NewReader(`{"cwd": "`+h+`"}`), &out, &bytes.Buffer{})
		done <- out.String()
	}()
	select {
	case out := <-done:
		if out != "{}\n" {
			t.Errorf("output %q", out)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("hook blocked on the FIFO log")
	}
	if runs := LastRuns(h, ""); len(runs) != 0 {
		t.Errorf("runs from a FIFO: %v", runs)
	}
}
