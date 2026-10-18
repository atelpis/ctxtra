package hookjson

import (
	"encoding/json"
	"fmt"
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

// --remove deletes a file of ctxtra's own after a backup, but keeps a
// symlink and writes its target.
func TestOwnFileRemoved(t *testing.T) {
	h := testenv.Home(t)
	on := fakeBinary(t, h)
	c := Config{Agent: "copilot", Path: filepath.Join(h, ".copilot", "hooks", "ctxtra.json"),
		Events: []Event{{Name: "sessionStart"}}, Flat: true, Version: 1, Own: true}
	var r agent.Report
	if err := c.Apply(on, &r); err != nil {
		t.Fatal(err)
	}
	if err := c.Apply(remove, &r); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(c.Path); !os.IsNotExist(err) {
		t.Errorf("not removed: %v", err)
	}
	if b, _ := os.ReadDir(filepath.Join(h, ".ctxtra", "backups")); len(b) == 0 {
		t.Error("no backup")
	}
	target := filepath.Join(h, "dotfiles", "ctxtra.json")
	testenv.Write(t, target, "{}")
	if err := os.Symlink(target, c.Path); err != nil {
		t.Fatal(err)
	}
	if err := c.Apply(on, &r); err != nil {
		t.Fatal(err)
	}
	if err := c.Apply(remove, &r); err != nil {
		t.Fatal(err)
	}
	if st, err := os.Lstat(c.Path); err != nil || st.Mode()&os.ModeSymlink == 0 {
		t.Errorf("symlink not kept: %v", err)
	}
	if b, err := os.ReadFile(target); err != nil || strings.Contains(string(b), "ctxtra") {
		t.Errorf("target: %v\n%s", err, b)
	}
}

// With slots, setup writes an entry per slot in the place of the old single
// entry, keeps another tool's entries around them, and --remove removes all.
func TestSlots(t *testing.T) {
	h := testenv.Home(t)
	on := fakeBinary(t, h)
	single := Config{Agent: "claude", Path: filepath.Join(h, ".claude", "settings.json"), Events: events}
	c := single
	c.Slots = 4
	testenv.Write(t, c.Path, otherTool)
	var r agent.Report
	if err := single.Apply(on, &r); err != nil {
		t.Fatal(err)
	}
	// Another tool's hook after the old single entry.
	b, _ := os.ReadFile(c.Path)
	after := strings.Replace(string(b), `"SubagentStart": [`, `"SubagentStart": [{"hooks": [{"type": "command", "command": "late-tool"}]}, `, 1)
	testenv.Write(t, c.Path, after)
	if st := c.Status(); st.OK || !strings.Contains(strings.Join(st.Details, "\n"), "SessionStart: 0 of the 4 ctxtra hooks (run ctxtra setup)") {
		t.Errorf("old single entry: %+v", st)
	}
	r = agent.Report{}
	if err := c.Apply(on, &r); err != nil || len(r.Changes) != 1 || len(r.Warnings) != 0 {
		t.Fatalf("upgrade: %+v, %v", r, err)
	}
	b, _ = os.ReadFile(c.Path)
	s := string(b)
	for _, ev := range []string{"SessionStart", "SubagentStart"} {
		for n := range 4 {
			if !strings.Contains(s, fmt.Sprintf("hook claude %s --slot %d;", ev, n)) {
				t.Errorf("no slot %d for %s:\n%s", n, ev, s)
			}
		}
		if strings.Contains(s, "hook claude "+ev+";") {
			t.Errorf("the old %s entry is still there:\n%s", ev, s)
		}
	}
	if strings.Index(s, "other-tool") > strings.Index(s, "SessionStart --slot 0") ||
		strings.Index(s, "late-tool") > strings.Index(s, "SubagentStart --slot 0") {
		t.Errorf("want [other, ours] and [late, ours]:\n%s", s)
	}
	if strings.Count(s, `"matcher": "startup"`) != 4 {
		t.Errorf("want the matcher in each SessionStart entry:\n%s", s)
	}
	if st := c.Status(); !st.OK || st.State != "set up for [SessionStart SubagentStart], runs ~/go/bin/ctxtra (ok)" {
		t.Errorf("status %+v", st)
	}
	r = agent.Report{}
	if err := c.Apply(on, &r); err != nil || len(r.Changes) != 0 {
		t.Errorf("second setup changed %v (err %v)", r.Changes, err)
	}
	// A slot removed by hand: doctor says so, and setup puts it back.
	testenv.Write(t, c.Path, strings.Replace(s, "SessionStart --slot 2;", "SessionStart --slot 1;", 1))
	if st := c.Status(); st.OK {
		t.Errorf("slot 2 missing: %+v", st)
	}
	if err := c.Apply(on, &r); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(c.Path); string(b) != s {
		t.Errorf("setup didn't restore the slots:\n%s", b)
	}
	if err := c.Apply(remove, &r); err != nil {
		t.Fatal(err)
	}
	b, _ = os.ReadFile(c.Path)
	if strings.Contains(string(b), "hook claude") || !strings.Contains(string(b), "other-tool") || !strings.Contains(string(b), "late-tool") {
		t.Errorf("remove:\n%s", b)
	}
}

func TestContextLimit(t *testing.T) {
	h := testenv.Home(t)
	c := Config{Agent: "codex", Path: filepath.Join(h, ".codex", "hooks.json"), Events: events, ContextLimit: 250000}
	var r agent.Report
	if err := c.Apply(fakeBinary(t, h), &r); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(c.Path)
	if strings.Count(string(b), `"additionalContextLimit": 250000`) != 2 {
		t.Errorf("limits:\n%s", b)
	}
	if st := c.Status(); !st.OK || len(st.Details) != 0 {
		t.Errorf("status %+v", st)
	}
	// An entry of an older version, without the limit: doctor says so, and
	// setup adds it.
	testenv.Write(t, c.Path, strings.Replace(string(b), `,
            "additionalContextLimit": 250000`, "", 1))
	if st := c.Status(); st.OK || len(st.Details) != 1 ||
		st.Details[0] != "SessionStart: a ctxtra hook without additionalContextLimit 250000, which cuts the text (run ctxtra setup)" {
		t.Errorf("without the limit: %+v", st)
	}
	r = agent.Report{}
	if err := c.Apply(fakeBinary(t, h), &r); err != nil || len(r.Changes) != 1 || !c.Status().OK {
		t.Errorf("setup: %+v, %v, %+v", r, err, c.Status())
	}
}
