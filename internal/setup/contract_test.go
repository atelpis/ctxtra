package setup

import (
	"flag"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/atelpis/ctxtra/internal/agent/registry"
	"github.com/atelpis/ctxtra/internal/testenv"
)

var update = flag.Bool("update", false, "rewrite testdata/contract with what setup writes")

// TestContract: what setup writes is the contract with the agents
// (docs/spec.md, Setup compatibility). A difference from testdata/contract
// is a change of the contract: see the checklist there before -update.
func TestContract(t *testing.T) {
	clients := testenv.Clients(t)
	for _, a := range registry.All() {
		t.Run(a.Name(), func(t *testing.T) {
			h := testenv.Home(t)
			if err := testenv.CheckPath(h); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", t.TempDir())
			files := []string{a.ConfigPath()}
			if p, ok := a.(interface{ PluginPath() string }); ok {
				files = append(files, p.PluginPath())
			}
			for _, f := range files {
				if !inside(h, f) {
					t.Fatalf("%s is outside the test home %s", f, h)
				}
			}
			o := Options{Agents: []string{a.Name()}}
			o.Binary = filepath.Join(h, "bin", "ctxtra")
			r, err := Run(o)
			if err != nil || len(r.Errors) > 0 {
				t.Fatalf("setup: %v, %v", err, r.Errors)
			}
			for _, f := range r.Changes {
				if !inside(h, f) {
					t.Errorf("setup wrote %s, outside the test home %s", f, h)
				}
			}
			got := homeFiles(t, h)
			want := filepath.Join("testdata", "contract", a.Name())
			if *update {
				if err := os.RemoveAll(want); err != nil {
					t.Fatal(err)
				}
				for rel, content := range got {
					testenv.Write(t, filepath.Join(want, rel), content)
				}
			}
			golden := tree(t, want)
			for rel, content := range got {
				if g, ok := golden[rel]; !ok {
					t.Errorf("setup writes %s, which isn't in %s", rel, want)
				} else if g != content {
					t.Errorf("%s differs from %s:\n%s", rel, filepath.Join(want, rel), content)
				}
			}
			for rel := range golden {
				if _, ok := got[rel]; !ok {
					t.Errorf("setup doesn't write %s", rel)
				}
			}
			if t.Failed() {
				return
			}
			// The CLI contract test runs the fixtures, so it runs this setup
			// only when one has its files.
			if !slices.ContainsFunc(clients, func(c testenv.Client) bool {
				return c.Agent == a.Name() && maps.Equal(tree(t, c.Dir), golden)
			}) {
				t.Errorf("no fixture in %s has the files of %s: add one, with its client.json (docs/spec.md, Setup compatibility)",
					testenv.ClientsDir(), want)
			}
		})
	}
}

// tree is every file in dir but a fixture's client.json, by path from dir.
func tree(t *testing.T, dir string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(dir, path)
		if rel == "client.json" {
			return nil
		}
		b, err := os.ReadFile(path)
		out[filepath.ToSlash(rel)] = string(b)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// homeFiles is every file in the home folder h but ~/.ctxtra, by path from
// h, with h in the content replaced by @@HOME@@.
func homeFiles(t *testing.T, h string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(h, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(h, path)
		switch {
		case rel == ".ctxtra":
			return filepath.SkipDir
		case d.IsDir():
			return nil
		case !d.Type().IsRegular():
			t.Errorf("%s is not a regular file", rel)
			return nil
		}
		b, err := os.ReadFile(path)
		out[filepath.ToSlash(rel)] = strings.ReplaceAll(string(b), h, "@@HOME@@")
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func inside(h, path string) bool {
	rel, err := filepath.Rel(h, path)
	return err == nil && filepath.IsLocal(rel)
}
