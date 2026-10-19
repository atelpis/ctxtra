//go:build unix

package cli

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/atelpis/ctxtra/internal/setup"
	"github.com/atelpis/ctxtra/internal/state"
)

// A FIFO in place of a config file, or of setup.json, doesn't block the
// notices: they don't read it, and say so.
func TestNoticesFIFO(t *testing.T) {
	h, bin := noticeHome(t)
	setUp(t, bin, "claude", "hermes")
	for _, p := range []string{filepath.Join(h, ".claude", "settings.json"), filepath.Join(h, ".hermes", "config.yaml"), state.SetupsPath()} {
		os.Remove(p)
		if err := syscall.Mkfifo(p, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	done := make(chan []string)
	go func() { done <- notices() }()
	select {
	case got := <-done:
		want := []string{
			"~/.ctxtra/setup.json is unreadable",
			"claude: can't check the setup in ~/.claude/settings.json: ",
			"hermes: can't tell whether the ctxtra plugin is enabled: ",
		}
		if len(got) != len(want) {
			t.Fatalf("notices %q", got)
		}
		for i, w := range want {
			if !strings.HasPrefix(got[i], w) || !strings.HasSuffix(got[i], "is not a regular file") && i > 0 {
				t.Errorf("notice %q, want %q...", got[i], w)
			}
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the notices block on a FIFO")
	}
}

// A FIFO in place of setup.lock blocks neither the notices, whose record
// of a new agent is skipped, nor setup, which fails.
func TestLockFIFO(t *testing.T) {
	h, bin := noticeHome(t)
	setUp(t, bin, "claude")
	if err := os.MkdirAll(filepath.Join(h, ".cursor"), 0o755); err != nil {
		t.Fatal(err)
	}
	os.Remove(state.SetupsLock())
	if err := syscall.Mkfifo(state.SetupsLock(), 0o400); err != nil {
		t.Fatal(err)
	}
	done := make(chan error)
	go func() {
		notices()
		o := setup.Options{Agents: []string{"claude"}}
		o.Binary = bin
		_, err := setup.Run(o)
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Error("setup with a FIFO lock: no error")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("a FIFO lock blocks")
	}
	wantNotices(t, "not recorded", "cursor: found since the last ctxtra setup; to add ctxtra to it, run ctxtra setup --agent cursor")
}
