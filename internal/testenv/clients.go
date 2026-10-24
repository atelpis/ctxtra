package testenv

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/atelpis/ctxtra/internal/chunk"
	"go.yaml.in/yaml/v3"
)

// Client is a client fixture: what one version of ctxtra set up for one
// agent, in testdata/clients/<id>/<agent> at the repository root, with the
// home folder written as @@HOME@@ (docs/spec.md, Setup compatibility).
// client.json has the limits that the client itself sets, and where its
// answer has the text.
type Client struct {
	ID  string `json:"-"`
	Dir string `json:"-"`

	Agent string `json:"agent"`
	// Slots is how many hooks (callbacks for Hermes) of an event the client
	// runs, each for one part of the text; 0 when it doesn't have slots.
	Slots int `json:"slots,omitempty"`
	// Part is the most text of one part, in one hook answer or one element
	// of a list in it, that the agent shows in full (measured); nil when the
	// text comes in one. In json, it is the hook's whole answer, in bytes.
	Part *Limit `json:"part,omitempty"`
	// Limit is the most text that the client lets the agent show, nil for
	// none.
	Limit *Limit `json:"limit,omitempty"`
	// Protocol is the ctxtra_plugin that a plugin sends, 0 for none.
	Protocol int `json:"protocol,omitempty"`
	// Reads is the answer field with the text that a hook command's client
	// reads, a dotted path of keys; key[] has a list, with a part per
	// element. Plugins leave it out: their code is the reader.
	Reads  string   `json:"reads,omitempty"`
	Events []string `json:"events"`
}

// Limit is an amount of text in a unit: chars, bytes, utf16 or json (see
// chunk.Unit).
type Limit struct {
	Value int    `json:"value"`
	Unit  string `json:"unit"`
}

// ChunkUnit is the unit of l.
func (l Limit) ChunkUnit() (chunk.Unit, bool) {
	u, ok := map[string]chunk.Unit{"chars": chunk.Chars, "bytes": chunk.Bytes, "utf16": chunk.UTF16, "json": chunk.JSON}[l.Unit]
	return u, ok
}

// ClientsDir is testdata/clients at the repository root.
func ClientsDir() string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(file), "..", "..", "testdata", "clients")
}

// Clients are all client fixtures, by id, then agent.
func Clients(t *testing.T) []Client {
	t.Helper()
	dirs, err := filepath.Glob(filepath.Join(ClientsDir(), "*", "*", "client.json"))
	if err != nil || len(dirs) == 0 {
		t.Fatalf("no client fixtures in %s (%v)", ClientsDir(), err)
	}
	var out []Client
	for _, f := range dirs {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		var c Client
		dec := json.NewDecoder(bytes.NewReader(b))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&c); err != nil {
			t.Fatalf("%s: %v", f, err)
		}
		c.Dir = filepath.Dir(f)
		c.ID = filepath.Base(filepath.Dir(c.Dir))
		if c.Agent != filepath.Base(c.Dir) || len(c.Events) == 0 || c.Slots < 0 || !valid(c.Limit) || !valid(c.Part) {
			t.Fatalf("%s: %+v", f, c)
		}
		out = append(out, c)
	}
	return out
}

func valid(l *Limit) bool {
	if l == nil {
		return true
	}
	_, ok := l.ChunkUnit()
	return ok && l.Value > 0
}

// Load writes the client's files into home, with @@HOME@@ replaced by
// home, and links bin at $HOME/bin/ctxtra, the binary that the files run.
func (c Client) Load(t *testing.T, home, bin string) {
	t.Helper()
	if err := CheckPath(home); err != nil {
		t.Fatal(err)
	}
	err := filepath.WalkDir(c.Dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(c.Dir, path)
		if rel == "client.json" {
			return nil
		}
		if err := CheckPath(rel); err != nil {
			return err
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		content := strings.ReplaceAll(string(b), "@@HOME@@", home)
		if err := parses(rel, content); err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
		Write(t, filepath.Join(home, rel), content)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(home, "bin", "ctxtra")
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(bin, link); err != nil {
		t.Fatal(err)
	}
}

// parses checks that a JSON or YAML file still is one.
func parses(rel, content string) error {
	var v any
	switch filepath.Ext(rel) {
	case ".json":
		return json.Unmarshal([]byte(content), &v)
	case ".yaml", ".yml":
		return yaml.Unmarshal([]byte(content), &v)
	}
	return nil
}

var safePath = regexp.MustCompile(`^[A-Za-z0-9_./-]+$`)

// CheckPath rejects a path that a fixture can't hold as it is: @@HOME@@
// is replaced as plain text, which is right only for characters that need
// no escaping in JSON, YAML, TypeScript, Python and shell.
func CheckPath(p string) error {
	if !safePath.MatchString(p) || slices.Contains(strings.Split(p, "/"), "..") {
		return fmt.Errorf("path %q has characters other than A-Z, a-z, 0-9, _, ., / and -, or a .. element", p)
	}
	return nil
}

// Releases is testdata/clients/releases.json at path: for each release tag,
// the id of the client fixture of each agent that the release sets up. A
// missing or empty file has no releases.
func Releases(path string) (map[string]map[string]string, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) || err == nil && len(bytes.TrimSpace(b)) == 0 {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out map[string]map[string]string
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&out); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return out, nil
}
