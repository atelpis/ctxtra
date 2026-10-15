package setup

import (
	"os"
	"path/filepath"
	"testing"

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
	if err != nil || len(r.Errors) > 0 || len(r.Agents) != 1 || r.Agents[0] != "cursor" || len(r.Changes) != 1 {
		t.Fatalf("report %+v, err %v", r, err)
	}
	if _, err := os.Stat(filepath.Join(h, ".cursor", "hooks.json")); !os.IsNotExist(err) {
		t.Error("dry run wrote files")
	}
}
