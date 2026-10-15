package hookjson

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/atelpis/ctxtra/internal/agent"
	"github.com/atelpis/ctxtra/internal/testenv"
)

const otherTool = `{
  "env": {"A": "<&>"},
  "hooks": {
    "SessionStart": [
      {"hooks": [{"type": "command", "command": "other-tool start >/dev/null && true"}]}
    ]
  },
  "zzz": 1.50
}
`

var events = []Event{{Name: "SessionStart", Matcher: "startup"}, {Name: "SubagentStart"}}

// fakeBinary creates an executable at ~/go/bin/ctxtra and returns the
// options to set up hooks that run it.
func fakeBinary(t *testing.T, h string) agent.Options {
	t.Helper()
	bin := filepath.Join(h, "go", "bin", "ctxtra")
	testenv.Write(t, bin, "#!/bin/sh\n")
	if err := os.Chmod(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	return agent.Options{Binary: bin}
}

var remove = agent.Options{Remove: true}

func TestApplyKeepsOtherEntries(t *testing.T) {
	h := testenv.Home(t)
	on := fakeBinary(t, h)
	c := Config{Agent: "claude", Path: filepath.Join(h, ".claude", "settings.json"), Events: events}
	testenv.Write(t, c.Path, otherTool)
	var r agent.Report
	if err := c.Apply(on, &r); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(c.Path)
	s := string(b)
	if strings.Index(s, `"env"`) > strings.Index(s, `"hooks"`) || strings.Index(s, `"hooks"`) > strings.Index(s, `"zzz"`) {
		t.Errorf("key order changed:\n%s", s)
	}
	if !strings.Contains(s, `"<&>"`) || !strings.Contains(s, ">/dev/null && true") || !strings.Contains(s, "1.50") {
		t.Errorf("values re-encoded:\n%s", s)
	}
	if strings.Index(s, "other-tool") > strings.Index(s, "hook claude SessionStart") {
		t.Errorf("want [other, ours]:\n%s", s)
	}
	if st := c.Status(); !st.OK || st.State != "set up for [SessionStart SubagentStart], runs ~/go/bin/ctxtra (ok)" {
		t.Errorf("status %+v", st)
	}
	// Idempotent.
	r = agent.Report{}
	if err := c.Apply(on, &r); err != nil || len(r.Changes) != 0 {
		t.Errorf("second setup changed %v (err %v)", r.Changes, err)
	}
	// Remove restores the original content, apart from formatting.
	if err := c.Apply(remove, &r); err != nil {
		t.Fatal(err)
	}
	var got, want any
	b, _ = os.ReadFile(c.Path)
	_ = json.Unmarshal(b, &got)
	_ = json.Unmarshal([]byte(otherTool), &want)
	gb, _ := json.Marshal(got)
	wb, _ := json.Marshal(want)
	if string(gb) != string(wb) {
		t.Errorf("remove: got %s, want %s", gb, wb)
	}
	if backups, _ := os.ReadDir(filepath.Join(h, ".ctxtra", "backups")); len(backups) != 2 {
		t.Errorf("want a backup per change, got %d", len(backups))
	}
}

func TestApplyMixedGroupAndSymlink(t *testing.T) {
	h := testenv.Home(t)
	on := fakeBinary(t, h)
	// A config that is a symlink into a dotfiles repo stays a symlink.
	real := filepath.Join(h, "dotfiles", "claude-settings.json")
	testenv.Write(t, real, "{}")
	c := Config{Agent: "claude", Path: filepath.Join(h, ".claude", "settings.json"), Events: events}
	if err := os.MkdirAll(filepath.Dir(c.Path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(real, c.Path); err != nil {
		t.Fatal(err)
	}
	var r agent.Report
	if err := c.Apply(on, &r); err != nil {
		t.Fatal(err)
	}
	if st, err := os.Lstat(c.Path); err != nil || st.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("symlink replaced: %v", err)
	}
	if b, _ := os.ReadFile(real); !strings.Contains(string(b), "hook claude SessionStart") {
		t.Errorf("target not updated: %s", b)
	}

	// A symlink to a missing file is left alone.
	d := Config{Agent: "codex", Path: filepath.Join(h, ".codex", "hooks.json"), Events: events}
	if err := os.MkdirAll(filepath.Dir(d.Path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(h, "dotfiles", "missing.json"), d.Path); err != nil {
		t.Fatal(err)
	}
	if err := d.Apply(on, &r); err == nil {
		t.Error("dangling symlink: no error")
	}
	if st, err := os.Lstat(d.Path); err != nil || st.Mode()&os.ModeSymlink == 0 {
		t.Error("dangling symlink replaced")
	}

	// A hand-written group with our handler and another tool's handler: the
	// other handler is kept on setup and remove, and positions don't move
	// on setup (Codex trust keys are positional).
	ours := agent.Command(on.Binary, "claude", "SessionStart")
	mixed, _ := json.Marshal(map[string]any{"hooks": map[string]any{"SessionStart": []any{
		map[string]any{"hooks": []any{
			map[string]any{"type": "command", "command": ours},
			map[string]any{"type": "command", "command": "other-tool --flag"},
		}},
		map[string]any{"hooks": []any{
			map[string]any{"type": "command", "command": `/bin/sh -c 'f="$HOME/go/bin/ctxtra"; exec audit "$f"'`},
		}},
	}}})
	testenv.Write(t, real, string(mixed))
	for _, add := range []bool{true, false} {
		o := on
		o.Remove = !add
		if err := c.Apply(o, &r); err != nil {
			t.Fatal(err)
		}
		b, _ := os.ReadFile(real)
		s := string(b)
		if !strings.Contains(s, "other-tool --flag") || !strings.Contains(s, "exec audit") {
			t.Errorf("add=%v removed another tool's handler: %s", add, s)
		}
		if n := strings.Count(s, "hook claude SessionStart"); (add && n != 1) || (!add && n != 0) {
			t.Errorf("add=%v: %d ctxtra handlers: %s", add, n, s)
		}
		if add && strings.Index(s, "hook claude SessionStart") > strings.Index(s, "other-tool") {
			t.Errorf("setup moved our handler: %s", s)
		}
	}
}

// After the binary moves (for example from go install to brew), setup
// updates its entries in place, and Status reports a missing binary.
func TestBinaryMoved(t *testing.T) {
	h := testenv.Home(t)
	old := fakeBinary(t, h)
	c := Config{Agent: "codex", Path: filepath.Join(h, ".codex", "hooks.json"), Events: events}
	testenv.Write(t, c.Path, otherTool)
	var r agent.Report
	if err := c.Apply(old, &r); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(old.Binary); err != nil {
		t.Fatal(err)
	}
	if st := c.Status(); st.OK || !strings.Contains(st.State, "runs ~/go/bin/ctxtra (MISSING)") {
		t.Errorf("status %+v", st)
	}
	if err := c.Apply(agent.Options{Binary: "/opt/homebrew/bin/ctxtra"}, &r); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(c.Path)
	s := string(b)
	if strings.Contains(s, "go/bin") || strings.Count(s, "/opt/homebrew/bin/ctxtra") != 2 {
		t.Errorf("not updated in place:\n%s", s)
	}
	if strings.Index(s, "other-tool") > strings.Index(s, "/opt/homebrew") {
		t.Errorf("positions changed:\n%s", s)
	}
}

func TestFlatLayoutAndBadFiles(t *testing.T) {
	h := testenv.Home(t)
	on := fakeBinary(t, h)
	c := Config{Agent: "cursor", Path: filepath.Join(h, ".cursor", "hooks.json"), Events: []Event{{Name: "sessionStart"}}, Flat: true, Version: 1}
	var r agent.Report
	if err := c.Apply(on, &r); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(c.Path)
	if !strings.Contains(string(b), `"version": 1`) || !strings.Contains(string(b), `"sessionStart": [`+"\n"+`      {`+"\n"+`        "command"`) {
		t.Errorf("cursor layout:\n%s", b)
	}
	for _, bad := range []string{`{"hooks": []}`, `{"hooks": {"sessionStart": {}}}`, `[1]`, `{"a": 1, "a": 2}`} {
		testenv.Write(t, c.Path, bad)
		if err := c.Apply(on, &r); err == nil {
			t.Errorf("%s: no error", bad)
		}
		if b, _ := os.ReadFile(c.Path); string(b) != bad {
			t.Errorf("%s: changed to %s", bad, b)
		}
	}
}

// An entry written by an earlier version (cat instead of /bin/cat) is
// updated in place, not added a second time.
func TestOldCommandUpdatedInPlace(t *testing.T) {
	h := testenv.Home(t)
	on := fakeBinary(t, h)
	c := Config{Agent: "codex", Path: filepath.Join(h, ".codex", "hooks.json"), Events: events[:1]}
	old := `/bin/sh -c 'f=\"$HOME/go/bin/ctxtra\"; if [ -x \"$f\" ]; then exec \"$f\" hook codex SessionStart; else cat >/dev/null 2>&1; echo \"{}\"; fi'`
	testenv.Write(t, c.Path, `{"hooks": {"SessionStart": [{"hooks": [{"type": "command", "command": "`+old+`"}]}, {"hooks": [{"type": "command", "command": "other"}]}]}}`)
	var r agent.Report
	if err := c.Apply(on, &r); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(c.Path)
	s := string(b)
	if strings.Count(s, "hook codex SessionStart") != 1 || !strings.Contains(s, "/bin/cat") ||
		strings.Index(s, "/bin/cat") > strings.Index(s, `"other"`) {
		t.Errorf("not updated in place:\n%s", s)
	}
}

// Remove leaves a file without our entries untouched, and doesn't create one.
func TestRemoveWithoutOurEntries(t *testing.T) {
	h := testenv.Home(t)
	c := Config{Agent: "claude", Path: filepath.Join(h, ".claude", "settings.json"), Events: events}
	var r agent.Report
	if err := c.Apply(remove, &r); err != nil || len(r.Changes) != 0 {
		t.Fatalf("missing file: %v %v", r.Changes, err)
	}
	if _, err := os.Stat(c.Path); !os.IsNotExist(err) {
		t.Error("remove created the file")
	}
	const compact = `{"a":1,"hooks":{"SessionStart":[{"hooks":[{"type":"command","command":"other"}]}]}}`
	testenv.Write(t, c.Path, compact)
	if err := c.Apply(remove, &r); err != nil || len(r.Changes) != 0 {
		t.Fatalf("other entries: %v %v", r.Changes, err)
	}
	if b, _ := os.ReadFile(c.Path); string(b) != compact {
		t.Errorf("reformatted: %s", b)
	}
}
