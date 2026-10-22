package setup

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/atelpis/ctxtra/internal/fsutil"
	"github.com/atelpis/ctxtra/internal/state"
	"github.com/atelpis/ctxtra/internal/testenv"
)

func TestRun(t *testing.T) {
	h := testenv.Home(t)
	o := Options{Agents: []string{"vim"}}
	o.Binary, o.DryRun = "/bin/ctxtra", true
	if _, err := Run(o); err == nil {
		t.Error("unknown agent accepted")
	}
	o.Agents, o.Binary = []string{"claude"}, "ctxtra"
	if _, err := Run(o); err == nil {
		t.Error("relative binary path accepted")
	}
	// Without --agent: only the agents found on this machine.
	if err := os.MkdirAll(filepath.Join(h, ".cursor"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", t.TempDir())
	o.Agents, o.Binary = nil, "/bin/ctxtra"
	r, err := Run(o)
	if err != nil || len(r.Errors) > 0 || len(r.Agents) != 1 || r.Agents[0].Name() != "cursor" || len(r.Agents[0].Changes) != 1 || len(r.Changes) != 1 {
		t.Fatalf("report %+v, err %v", r, err)
	}
	if _, err := os.Stat(filepath.Join(h, ".cursor", "hooks.json")); !os.IsNotExist(err) {
		t.Error("dry run wrote files")
	}
	if _, err := os.Stat(filepath.Join(h, ".ctxtra")); !os.IsNotExist(err) {
		t.Error("dry run wrote setup.json or its lock")
	}
}

// A setup waits for another one: the lock covers its config changes and
// setup.json.
func TestRunLocks(t *testing.T) {
	h := testenv.Home(t)
	t.Setenv("PATH", t.TempDir())
	unlock, err := fsutil.Lock(state.SetupsLock())
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error)
	go func() {
		o := Options{Agents: []string{"claude"}}
		o.Binary = "/bin/ctxtra"
		_, err := Run(o)
		done <- err
	}()
	select {
	case err := <-done:
		t.Fatalf("setup ran while another held the lock: %v", err)
	case <-time.After(200 * time.Millisecond):
	}
	if _, err := os.Stat(filepath.Join(h, ".claude", "settings.json")); !os.IsNotExist(err) {
		t.Errorf("config changed under another's lock: %v", err)
	}
	unlock()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if s, ok, err := state.LoadSetups(); !ok || err != nil || !s.Known("claude", filepath.Join(h, ".claude", "settings.json")) {
		t.Errorf("setup.json %+v, %v, %v", s, ok, err)
	}
}
