package fixture

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

var safePath = regexp.MustCompile(`^[A-Za-z0-9_./-]+$`)

// LoadClient writes a client fixture, testdata/clients/<id>/<agent> at the
// repository root, into the home folder, with @@HOME@@ replaced by it, and
// links the ctxtra binary under test at $HOME/bin/ctxtra, the binary that
// the fixture runs. A small copy of testenv's Client.Load in the main
// module, which this module can't import; that one also checks that the
// YAML files parse.
func (f *Fixture) LoadClient(dir string) {
	f.t.Helper()
	check := func(p string) error {
		if !safePath.MatchString(p) || slices.Contains(strings.Split(p, "/"), "..") {
			return fmt.Errorf("path %q has characters other than A-Z, a-z, 0-9, _, ., / and -, or ..", p)
		}
		return nil
	}
	if err := check(f.Home); err != nil {
		f.t.Fatal(err)
	}
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(dir, path)
		if rel == "client.json" {
			return nil
		}
		if err := check(rel); err != nil {
			return err
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		content := strings.ReplaceAll(string(b), "@@HOME@@", f.Home)
		if filepath.Ext(rel) == ".json" && !json.Valid([]byte(content)) {
			return fmt.Errorf("%s: not JSON with the home folder in it", path)
		}
		f.Write(rel, content)
		return nil
	})
	if err != nil {
		f.t.Fatal(err)
	}
	f.mkdir("bin")
	if err := os.Symlink(f.Bin, f.Path("bin/ctxtra")); err != nil {
		f.t.Fatal(err)
	}
}
