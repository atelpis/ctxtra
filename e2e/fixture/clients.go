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

// Client is a client fixture, with the limits from its client.json that the
// e2e tests use. testenv.Client in the main module has the whole file.
type Client struct {
	ID, Agent, Dir string
	// Slots is how many hooks or callbacks of an event the client runs, 0
	// without slots.
	Slots int
	// Limit is the most text that the client lets the agent show, nil for
	// none.
	Limit *struct {
		Value int
		Unit  string
	}
}

// Clients are the client fixtures in root, testdata/clients at the
// repository root, by id, then agent.
func Clients(root string) ([]Client, error) {
	files, err := filepath.Glob(filepath.Join(root, "*", "*", "client.json"))
	if err != nil || len(files) == 0 {
		return nil, fmt.Errorf("no client fixtures in %s (%v)", root, err)
	}
	var out []Client
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			return nil, err
		}
		c := Client{Dir: filepath.Dir(f)}
		if err := json.Unmarshal(b, &c); err != nil {
			return nil, fmt.Errorf("%s: %w", f, err)
		}
		c.ID = filepath.Base(filepath.Dir(c.Dir))
		if c.Agent != filepath.Base(c.Dir) {
			return nil, fmt.Errorf("%s: agent %q", f, c.Agent)
		}
		out = append(out, c)
	}
	return out, nil
}
