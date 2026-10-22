package testenv

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
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
	if !ids["0.1"] {
		t.Error("no 0.1 fixtures")
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

// Every release in releases.json names a tag and, for each agent, a client
// fixture that exists. A missing or empty file has no releases.
func TestReleases(t *testing.T) {
	releases, err := Releases(filepath.Join(ClientsDir(), "releases.json"))
	if err != nil {
		t.Fatal(err)
	}
	tag := regexp.MustCompile(`^v\d+\.\d+\.\d+$`)
	for r, agents := range releases {
		if !tag.MatchString(r) || len(agents) == 0 {
			t.Errorf("release %q: %v", r, agents)
		}
		for agent, id := range agents {
			if _, err := os.Stat(filepath.Join(ClientsDir(), id, agent, "client.json")); err != nil {
				t.Errorf("%s: %s: no fixture %s: %v", r, agent, id, err)
			}
		}
	}
	dir := t.TempDir()
	Write(t, filepath.Join(dir, "empty.json"), "\n")
	Write(t, filepath.Join(dir, "bad.json"), `{"v0.1.0": ["claude"]}`)
	for name, ok := range map[string]bool{"missing.json": true, "empty.json": true, "bad.json": false} {
		if r, err := Releases(filepath.Join(dir, name)); (err == nil) != ok || len(r) != 0 {
			t.Errorf("%s: %v, %v", name, r, err)
		}
	}
}
