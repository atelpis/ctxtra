package testenv

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Every client fixture loads: its JSON and YAML files parse with the home
// folder in place of @@HOME@@, and $HOME/bin/ctxtra is the binary.
func TestClients(t *testing.T) {
	ids := map[string]bool{}
	for _, c := range Clients(t) {
		ids[c.ID] = true
		h := Home(t)
		c.Load(t, h, "/bin/ctxtra")
		if target, err := os.Readlink(filepath.Join(h, "bin", "ctxtra")); err != nil || target != "/bin/ctxtra" {
			t.Errorf("%s/%s: bin/ctxtra links to %q, %v", c.ID, c.Agent, target, err)
		}
		n := 0
		err := filepath.WalkDir(h, func(path string, d fs.DirEntry, err error) error {
			if err != nil || !d.Type().IsRegular() {
				return err
			}
			n++
			b, err := os.ReadFile(path)
			if strings.Contains(string(b), "@@HOME@@") {
				t.Errorf("%s/%s: %s still has @@HOME@@", c.ID, c.Agent, path)
			}
			return err
		})
		if err != nil || n == 0 {
			t.Errorf("%s/%s: %d files, %v", c.ID, c.Agent, n, err)
		}
	}
	for _, id := range []string{"pre-v1", "v1"} {
		if !ids[id] {
			t.Errorf("no %s fixtures", id)
		}
	}
	for _, p := range []string{"/home/a b", "/home/\tx", "/home/ü", `/home/"x"`, "/home/$x", "/home/../x", ""} {
		if CheckPath(p) == nil {
			t.Errorf("%q accepted", p)
		}
	}
	if err := CheckPath("/var/folders/q4/b_w7/T/Test-1.x/001"); err != nil {
		t.Error(err)
	}
}
