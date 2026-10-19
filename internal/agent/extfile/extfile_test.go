package extfile

import (
	"path/filepath"
	"strings"
	"testing"

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
