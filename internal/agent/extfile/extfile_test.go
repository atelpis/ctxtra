package extfile

import (
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/atelpis/ctxtra/internal/agent"
	"github.com/atelpis/ctxtra/internal/testenv"
)

// The file says its protocol version on its third line. A file without
// the line is from before files said: version 0, or for a Python plugin
// the ctxtra_plugin that it sends.
func TestProtocol(t *testing.T) {
	h := testenv.Home(t)
	ts := Content(TypeScript, filepath.Join(h, "go", "bin", "ctxtra"), 1, "const bin = %s;\n")
	if want := "// @ctxtra-managed\n// binary: $HOME/go/bin/ctxtra\n// protocol: 1\nconst bin = join(homedir(), \"go/bin/ctxtra\");\n"; ts != want {
		t.Errorf("content:\n%s\nwant:\n%s", ts, want)
	}
	py := Content(Python, "/opt/ctxtra", 3, "BIN = %s\nx = {\"ctxtra_plugin\": 9}\n")
	if !strings.HasPrefix(py, "# @ctxtra-managed\n# binary: /opt/ctxtra\n# protocol: 3\nBIN = \"/opt/ctxtra\"\n") {
		t.Errorf("python content:\n%s", py)
	}
	noLine := func(s string) string {
		lines := strings.SplitAfter(s, "\n")
		return strings.Join(append(lines[:2:2], lines[3:]...), "")
	}
	for name, c := range map[string]struct {
		content string
		version int
		ok      bool
	}{
		"ts":             {ts, 1, true},
		"python":         {py, 3, true},
		"ts, no line":    {noLine(ts), 0, true},
		"hermes v2":      {noLine(py), 9, true},
		"hermes v1":      {"# @ctxtra-managed\n# binary: /opt/ctxtra\nBIN = \"/opt/ctxtra\"\n", 0, true},
		"bad line":       {"// @ctxtra-managed\n// binary: /x\n// protocol: x\n", 0, false},
		"someone else's": {"// protocol: 1\n", 0, false},
	} {
		path := filepath.Join(h, "ext", name)
		testenv.Write(t, path, c.content)
		if v, ok := Protocol(path); v != c.version || ok != c.ok {
			t.Errorf("%s: %d, %v; want %d, %v", name, v, ok, c.version, c.ok)
		}
	}
	if _, ok := Protocol(filepath.Join(h, "missing")); ok {
		t.Error("missing file: ok")
	}
}

// CheckSetup tells about the binary that the extension runs, an older
// protocol that gets less, and an extension that is gone.
func TestCheckSetup(t *testing.T) {
	h := testenv.Home(t)
	e := Ext{Path: filepath.Join(h, "ext", "ctxtra.ts"), Agent: "pi", Noun: "extension",
		Legacy: func(v int) (agent.Delivery, bool) { return agent.Delivery{Title: "Pi", Capacity: 9000}, v < 1 }}
	if n, ours := CheckSetup(e, "", false); n != nil || ours {
		t.Errorf("not set up: %q, %v", n, ours)
	}
	if n, _ := CheckSetup(e, "", true); len(n) != 1 || !strings.HasPrefix(n[0], "ctxtra's extension is gone from ~/ext/ctxtra.ts") {
		t.Errorf("gone: %q", n)
	}
	testenv.Write(t, e.Path, Content(TypeScript, filepath.Join(h, "gone", "ctxtra"), 1, "const bin = %s;\n"))
	if n, ours := CheckSetup(e, "", false); !ours || len(n) != 1 || n[0] != "the extension runs ~/gone/ctxtra, which doesn't exist; run ctxtra setup --agent pi" {
		t.Errorf("missing binary: %q, %v", n, ours)
	}
	testenv.Write(t, e.Path, "// @ctxtra-managed\nconst bin = 1;\n")
	if n, _ := CheckSetup(e, "", false); !slices.Equal(n, []string{
		"~/ext/ctxtra.ts doesn't say which binary it runs; run ctxtra setup --agent pi",
		"set up by an older ctxtra, with Pi's limit of 9,000 characters; run ctxtra setup --agent pi",
	}) {
		t.Errorf("unknown version: %q", n)
	}
}

// A plugin with CRLF line ends, as a checkout or an editor may write it,
// still says its binary and protocol: no notice.
func TestCRLF(t *testing.T) {
	for _, c := range testenv.Clients(t) {
		if c.ID != "v1" || c.Protocol == 0 {
			continue
		}
		h := testenv.Home(t)
		bin := filepath.Join(h, "real", "ctxtra")
		testenv.Write(t, bin, "#!/bin/sh\n")
		if err := os.Chmod(bin, 0o755); err != nil {
			t.Fatal(err)
		}
		c.Load(t, h, bin)
		var plugin string
		filepath.WalkDir(h, func(p string, d fs.DirEntry, err error) error {
			if ext := filepath.Ext(p); err == nil && (ext == ".ts" || ext == ".mjs" || ext == ".py") {
				plugin = p
			}
			return err
		})
		b, err := os.ReadFile(plugin)
		if err != nil {
			t.Fatalf("%s: %v", c.Agent, err)
		}
		testenv.Write(t, plugin, strings.ReplaceAll(string(b), "\n", "\r\n"))
		if n, ours := CheckSetup(Ext{Path: plugin, Agent: c.Agent, Noun: "plugin"}, "", false); len(n) != 0 || !ours {
			t.Errorf("%s: %q, %v", c.Agent, n, ours)
		}
		if v, ok := Protocol(plugin); !ok || v != c.Protocol {
			t.Errorf("%s: protocol %d, %v", c.Agent, v, ok)
		}
	}
}
