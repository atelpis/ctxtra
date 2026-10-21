//go:build unix

package cli

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/atelpis/ctxtra/internal/agent"
	"github.com/atelpis/ctxtra/internal/agent/claude"
	"github.com/atelpis/ctxtra/internal/setup"
	"github.com/atelpis/ctxtra/internal/testenv"
)

// A FIFO among the rules doesn't block the search for an agent's own
// instruction files: it is listed as a file that can't be read, and import
// doesn't start.
func TestNativeFilesFIFO(t *testing.T) {
	h := testenv.Home(t)
	testenv.Write(t, filepath.Join(h, ".claude", "CLAUDE.md"), "rules\n")
	testenv.Write(t, filepath.Join(h, ".claude", "rules", "go.md"), " \n")
	if err := syscall.Mkfifo(filepath.Join(h, ".claude", "rules", "stuck.md"), 0o600); err != nil {
		t.Fatal(err)
	}
	done := make(chan []agent.NativeFile)
	go func() { done <- claude.Agent{}.NativeFiles() }()
	select {
	case files := <-done:
		if len(files) != 2 || files[0].Err != nil || files[1].Err == nil || filepath.Base(files[1].Path) != "stuck.md" {
			t.Errorf("files %+v", files)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("blocked on the FIFO")
	}
	binary = func() (string, []string, error) { return "/opt/homebrew/bin/ctxtra", nil, nil }
	t.Cleanup(func() { binary = setup.Binary })
	if code, _, e := run("setup", "--agent", "claude"); code != 0 {
		t.Fatalf("setup: %s", e)
	}
	code, out, e := run("import")
	if code != 1 || !strings.Contains(out, "stuck.md (Claude Code; can't read it: ") ||
		!strings.Contains(e, "can't read some of the files above: fix or move them, then run ctxtra import") {
		t.Errorf("import: %d %q %q", code, out, e)
	}
	if _, err := os.Stat(filepath.Join(h, ".ctxtra", "backups")); err == nil {
		t.Error("backed up with a file that can't be read")
	}
}

// A symlinked folder of rules that can't be read is listed as a file that
// can't be read; a broken symlink is left out, as Claude leaves it out.
func TestNativeFilesLockedLink(t *testing.T) {
	h := testenv.Home(t)
	testenv.Write(t, filepath.Join(h, "dotfiles", "locked", "rules", "team.md"), "team\n")
	testenv.Write(t, filepath.Join(h, ".claude", "rules", "go.md"), "go\n")
	rules := filepath.Join(h, ".claude", "rules")
	if os.Symlink(filepath.Join(h, "dotfiles", "locked", "rules"), filepath.Join(rules, "shared")) != nil ||
		os.Symlink(filepath.Join(h, "gone"), filepath.Join(rules, "gone.md")) != nil {
		t.Fatal("symlink")
	}
	locked := filepath.Join(h, "dotfiles", "locked")
	if err := os.Chmod(locked, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(locked, 0o755) })
	if _, err := os.Stat(filepath.Join(rules, "shared")); err == nil {
		t.Skip("the locked folder can be read (root?)")
	}
	files := claude.Agent{}.NativeFiles()
	if len(files) != 2 || files[0].Err != nil || filepath.Base(files[0].Path) != "go.md" ||
		files[1].Err == nil || filepath.Base(files[1].Path) != "shared" {
		t.Errorf("files %+v", files)
	}
}
