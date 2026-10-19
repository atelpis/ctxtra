//go:build unix

package dsh

import (
	"os"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/atelpis/ctxtra/internal/agent"
	"github.com/atelpis/ctxtra/internal/testenv"
)

// A FIFO in place of the patch file is refused, not waited on.
func TestPatchFIFO(t *testing.T) {
	testenv.Home(t)
	var a Agent
	if err := os.MkdirAll(dir(), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(a.ConfigPath(), 0o600); err != nil {
		t.Fatal(err)
	}
	done := make(chan error)
	go func() { done <- a.Setup(agent.Options{Binary: "/bin/ctxtra"}, &agent.Report{}) }()
	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), "not a regular file") {
			t.Errorf("setup: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("setup blocks on a FIFO")
	}
	if n := a.Check("", false); len(n) != 1 || !strings.HasPrefix(n[0], "can't check the setup in ~/.dsh/cordis.patch.yml: ") {
		t.Errorf("check: %q", n)
	}
}
