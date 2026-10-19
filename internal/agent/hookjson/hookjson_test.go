package hookjson

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/atelpis/ctxtra/internal/agent"
	"github.com/atelpis/ctxtra/internal/chunk"
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
	if st := c.Status("", false); !st.OK || st.State != "set up for [SessionStart SubagentStart], runs ~/go/bin/ctxtra (ok)" {
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
	ours := agent.Command(on.Binary, "claude", "SessionStart", nil)
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
	if st := c.Status("", false); st.OK || !strings.Contains(st.State, "runs ~/go/bin/ctxtra (MISSING)") {
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
	if st := c.Status("", false); st.OK || !strings.Contains(strings.Join(st.Details, "\n"), "SessionStart has 0 of the 4 ctxtra hooks; run ctxtra setup --agent claude") {
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
			if !strings.Contains(s, fmt.Sprintf("hook claude %s --slot %d --of 4;", ev, n)) {
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
	if st := c.Status("", false); !st.OK || st.State != "set up for [SessionStart SubagentStart], runs ~/go/bin/ctxtra (ok)" {
		t.Errorf("status %+v", st)
	}
	r = agent.Report{}
	if err := c.Apply(on, &r); err != nil || len(r.Changes) != 0 {
		t.Errorf("second setup changed %v (err %v)", r.Changes, err)
	}
	// A slot removed by hand: doctor says so, and setup puts it back.
	testenv.Write(t, c.Path, strings.Replace(s, "SessionStart --slot 2 --of 4;", "SessionStart --slot 1 --of 4;", 1))
	if st := c.Status("", false); st.OK {
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
	c := Config{Agent: "codex", Path: filepath.Join(h, ".codex", "hooks.json"), Events: events, ContextLimit: 250000,
		Legacy: func(_ string, _ agent.Hook, limited bool) (agent.Delivery, bool) {
			return agent.Delivery{Title: "Codex", Capacity: 9000, Unit: chunk.Bytes}, !limited
		},
		RepairNote: " (and trust)"}
	var r agent.Report
	if err := c.Apply(fakeBinary(t, h), &r); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(c.Path)
	if strings.Count(string(b), `"additionalContextLimit": 250000`) != 2 {
		t.Errorf("limits:\n%s", b)
	}
	if st := c.Status("", false); !st.OK || len(st.Details) != 0 {
		t.Errorf("status %+v", st)
	}
	if !c.Limited("SessionStart") || !c.Limited("SubagentStart") {
		t.Error("not limited as set up")
	}
	// An entry of an older version, without the limit: the hook sees it,
	// doctor says so, and setup adds it.
	testenv.Write(t, c.Path, strings.Replace(string(b), `,
            "additionalContextLimit": 250000`, "", 1))
	if st := c.Status("", false); st.OK || len(st.Details) != 1 ||
		st.Details[0] != "set up by an older ctxtra, with Codex's limit of 9,000 bytes; run ctxtra setup --agent codex (and trust)" {
		t.Errorf("without the limit: %+v", st)
	}
	if c.Limited("SessionStart") || !c.Limited("SubagentStart") {
		t.Error("limited without the limit")
	}
	r = agent.Report{}
	if err := c.Apply(fakeBinary(t, h), &r); err != nil || len(r.Changes) != 1 || !c.Status("", false).OK {
		t.Errorf("setup: %+v, %v, %+v", r, err, c.Status("", false))
	}
	// Without a ctxtra hook for the event, or without the file, nothing
	// shows the limit.
	if c.Limited("Stop") {
		t.Error("limited without a hook")
	}
	os.Remove(c.Path)
	if c.Limited("SessionStart") {
		t.Error("limited without the file")
	}
}

// message is an agent whose hook answer shows the user a message.
type message struct{}

func (message) MissingAnswer(event, msg string) string { return agent.SystemMessage(event, msg) }

// The message for a missing binary goes to slot 0 at session start. Entries
// of earlier versions, which print {}, are updated in place: Codex keys
// trust by position.
func TestMissingMessage(t *testing.T) {
	h := testenv.Home(t)
	on := fakeBinary(t, h)
	c := Config{Agent: "claude", Path: filepath.Join(h, ".claude", "settings.json"), Events: events, Slots: 3, Missing: message{}}
	old := Config{Agent: "claude", Path: c.Path, Events: events}
	var r agent.Report
	if err := old.Apply(on, &r); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(c.Path)
	// Slots of an earlier version, without --of.
	slotted := strings.ReplaceAll(string(b), `hook claude SessionStart;`, `hook claude SessionStart --slot 0;`)
	testenv.Write(t, c.Path, slotted)
	if err := c.Apply(on, &r); err != nil {
		t.Fatal(err)
	}
	b, _ = os.ReadFile(c.Path)
	s := string(b)
	if n := strings.Count(s, "systemMessage"); n != 1 || !strings.Contains(s, `SessionStart --slot 0 --of 3; else /bin/cat >/dev/null 2>&1; echo \"{\\\"systemMessage\\\"`) {
		t.Errorf("want the message in slot 0 of SessionStart only, found %d:\n%s", n, s)
	}
	if strings.Count(s, "hook claude") != 6 || strings.Contains(s, "--slot 0;") {
		t.Errorf("not updated in place:\n%s", s)
	}
	if st := c.Status("", false); !st.OK {
		t.Errorf("status %+v", st)
	}
	// A slot count other than the agent's is a problem for doctor.
	testenv.Write(t, c.Path, strings.ReplaceAll(s, "--of 3;", "--of 5;"))
	if st := c.Status("", false); st.OK {
		t.Errorf("--of 5 with 3 slots: %+v", st)
	}

	// One hook per event, after another tool's: the message takes the place
	// of the old entry.
	d := Config{Agent: "codex", Path: filepath.Join(h, ".codex", "hooks.json"), Events: events, Missing: message{}}
	testenv.Write(t, d.Path, otherTool)
	if err := (Config{Agent: d.Agent, Path: d.Path, Events: events}).Apply(on, &r); err != nil {
		t.Fatal(err)
	}
	b, _ = os.ReadFile(d.Path)
	before := string(b)
	r = agent.Report{}
	if err := d.Apply(on, &r); err != nil || len(r.Changes) != 1 {
		t.Fatalf("update: %+v, %v", r, err)
	}
	b, _ = os.ReadFile(d.Path)
	s = string(b)
	if strings.Count(s, "hook codex") != 2 || strings.Count(s, "systemMessage") != 1 ||
		strings.Index(s, "other-tool") > strings.Index(s, "hook codex SessionStart") {
		t.Errorf("not updated in place:\n%s", s)
	}
	if strings.Count(before, "{}") != strings.Count(s, "{}")+1 {
		t.Errorf("SubagentStart changed:\nbefore %s\nafter %s", before, s)
	}
}

// A ctxtra hook edited by hand is left alone, by setup and by --remove,
// and setup adds no second one: the session would get the text twice.
func TestChangedByHand(t *testing.T) {
	h := testenv.Home(t)
	on := fakeBinary(t, h)
	for _, c := range []Config{
		{Agent: "claude", Path: filepath.Join(h, ".claude", "settings.json"), Events: events, Slots: 3, Missing: message{}},
		{Agent: "codex", Path: filepath.Join(h, ".codex", "hooks.json"), Events: events, Missing: message{}},
		{Agent: "cursor", Path: filepath.Join(h, ".cursor", "hooks.json"), Events: []Event{{Name: "sessionStart"}}, Flat: true, Version: 1},
	} {
		var r agent.Report
		if err := c.Apply(on, &r); err != nil {
			t.Fatal(err)
		}
		b, _ := os.ReadFile(c.Path)
		edited := strings.Replace(string(b), `/bin/cat >/dev/null 2>&1; echo`, `/bin/cat >/dev/null 2>&1; echo edited;  echo`, 1)
		if edited == string(b) {
			t.Fatalf("%s: nothing to edit:\n%s", c.Agent, b)
		}
		testenv.Write(t, c.Path, edited)
		for _, o := range []agent.Options{on, remove} {
			err := c.Apply(o, &r)
			want := c.Path + ": a ctxtra hook was changed by hand; left alone. Restore or delete it, then run ctxtra setup --agent " + c.Agent
			if o.Remove {
				want = strings.Replace(want, "setup --agent", "setup --remove --agent", 1)
			}
			if err == nil || err.Error() != want {
				t.Errorf("%s, remove=%v: %v", c.Agent, o.Remove, err)
			}
			if b, _ := os.ReadFile(c.Path); string(b) != edited {
				t.Errorf("%s, remove=%v: changed:\n%s", c.Agent, o.Remove, b)
			}
		}
	}
	// Other spellings that run a ctxtra hook: the agent last, more spaces,
	// a separator after the agent, a line continuation, quoting.
	for _, cmd := range []string{
		"ctxtra hook claude",
		`ctxtra hook \\\n  claude SessionStart`,
		`ctxtra ho\\\nok claude SessionStart`,
		`ctxtra hook cla\\\nude SessionStart`,
		`ctxtra \"hook\" 'cl'aude SessionStart`,
		`ctxtra hook \\claude`,
		`/bin/sh -c 'f=\"$HOME/go/bin/ctxtra\"; exec \"$f\" hook  claude\tSessionStart'`,
		"~/go/bin/ctxtra hook claude; true",
	} {
		c := Config{Agent: "claude", Path: filepath.Join(h, ".claude", "settings.json"), Events: events, Slots: 3, Missing: message{}}
		content := `{"hooks": {"SessionStart": [{"hooks": [{"type": "command", "command": "` + cmd + `"}]}]}}`
		testenv.Write(t, c.Path, content)
		for _, o := range []agent.Options{on, remove} {
			var r agent.Report
			if err := c.Apply(o, &r); err == nil || !strings.Contains(err.Error(), "changed by hand") {
				t.Errorf("%q, remove=%v: %v", cmd, o.Remove, err)
			}
			if b, _ := os.ReadFile(c.Path); string(b) != content {
				t.Errorf("%q, remove=%v: changed:\n%s", cmd, o.Remove, b)
			}
		}
	}
	// Another tool's command that names ctxtra isn't one of ours.
	c := Config{Agent: "claude", Path: filepath.Join(h, "other", "settings.json"), Events: events}
	testenv.Write(t, c.Path, `{"hooks": {"SessionStart": [{"hooks": [{"type": "command", "command": "audit ctxtra hook codex x; hookclaude; claude hook"}]}]}}`)
	var r agent.Report
	if err := c.Apply(on, &r); err != nil {
		t.Errorf("another tool's command: %v", err)
	}
}

// Check tells what to do: an event without our hook, slots that are
// missing, and hooks that are gone from a file that setup.json lists. A
// file it can't parse is one notice.
func TestCheck(t *testing.T) {
	h := testenv.Home(t)
	on := fakeBinary(t, h)
	c := Config{Agent: "claude", Path: filepath.Join(h, ".claude", "settings.json"), Events: events, Slots: 4}
	if n := c.Check("", true); len(n) != 1 || !strings.HasPrefix(n[0], "ctxtra's hooks are gone from ~/.claude/settings.json") {
		t.Errorf("gone: %q", n)
	}
	if n := c.Check("", false); len(n) != 0 {
		t.Errorf("never set up: %q", n)
	}
	var r agent.Report
	if err := c.Apply(on, &r); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(c.Path)
	var data map[string]map[string][]any
	if err := json.Unmarshal(b, &data); err != nil {
		t.Fatal(err)
	}
	data["hooks"]["SessionStart"] = data["hooks"]["SessionStart"][:3]
	delete(data["hooks"], "SubagentStart")
	b, _ = json.Marshal(data)
	testenv.Write(t, c.Path, string(b))
	want := []string{"SessionStart has 3 of the 4 ctxtra hooks; run ctxtra setup --agent claude", "SubagentStart has no ctxtra hook; run ctxtra setup --agent claude"}
	if n := c.Check("", false); !slices.Equal(n, want) {
		t.Errorf("incomplete: %q", n)
	}
	testenv.Write(t, c.Path, "{")
	if n := c.Check("", false); len(n) != 1 || !strings.HasPrefix(n[0], "can't check the setup in ~/.claude/settings.json: ") {
		t.Errorf("broken: %q", n)
	}
}

// A ctxtra hook edited by hand: setup and --remove leave the file alone, so
// Check says to restore or delete it, set up or not in setup.json.
func TestCheckChangedByHand(t *testing.T) {
	h := testenv.Home(t)
	c := Config{Agent: "cursor", Path: filepath.Join(h, ".cursor", "hooks.json"), Events: []Event{{Name: "sessionStart"}}, Flat: true, Version: 1}
	var r agent.Report
	if err := c.Apply(fakeBinary(t, h), &r); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(c.Path)
	testenv.Write(t, c.Path, strings.Replace(string(b), `echo \"{}\"`, `echo \"{ }\"`, 1))
	want := []string{"~/.cursor/hooks.json: a ctxtra hook was changed by hand; restore or delete it, then run ctxtra setup --agent cursor"}
	for _, known := range []bool{true, false} {
		if n := c.Check("", known); !slices.Equal(n, want) {
			t.Errorf("known %v: %q", known, n)
		}
	}
}

// Slots must be below the count each hook says, and there must be as many
// hooks as that count: a slot past it is a part that never arrives.
func TestCheckSlotRange(t *testing.T) {
	h := testenv.Home(t)
	c := Config{Agent: "claude", Path: filepath.Join(h, ".claude", "settings.json"), Events: events[:1], Slots: 4}
	var r agent.Report
	if err := c.Apply(fakeBinary(t, h), &r); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(c.Path)
	if n := c.Check("", false); len(n) != 0 {
		t.Fatalf("fresh: %q", n)
	}
	testenv.Write(t, c.Path, strings.Replace(string(b), "--slot 3 --of 4", "--slot 4 --of 4", 1))
	if n := c.Check("", false); !slices.Equal(n, []string{"SessionStart has 3 of the 4 ctxtra hooks; run ctxtra setup --agent claude"}) {
		t.Errorf("slot 4 of 4: %q", n)
	}
	// Hooks that say 5 slots, of which 4 are there.
	testenv.Write(t, c.Path, strings.ReplaceAll(string(b), "--of 4", "--of 5"))
	if n := c.Check("", false); !slices.Equal(n, []string{"SessionStart has 4 of the 5 ctxtra hooks; run ctxtra setup --agent claude"}) {
		t.Errorf("4 hooks of 5: %q", n)
	}
}

// Hooks with --of 0, which the hook refuses, are a broken setup.
func TestCheckOfZero(t *testing.T) {
	h := testenv.Home(t)
	c := Config{Agent: "claude", Path: filepath.Join(h, ".claude", "settings.json"), Events: events[:1], Slots: 4}
	var r agent.Report
	if err := c.Apply(fakeBinary(t, h), &r); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(c.Path)
	testenv.Write(t, c.Path, strings.ReplaceAll(string(b), "--of 4", "--of 0"))
	if n := c.Check("", false); !slices.Equal(n, []string{"SessionStart has 0 of the 4 ctxtra hooks; run ctxtra setup --agent claude"}) {
		t.Errorf("--of 0: %q", n)
	}
}

// A file nested too deep to decode is one notice, not a crash.
func TestCheckDeepFile(t *testing.T) {
	h := testenv.Home(t)
	c := Config{Agent: "cursor", Path: filepath.Join(h, ".cursor", "hooks.json"), Events: []Event{{Name: "sessionStart"}}, Flat: true}
	testenv.Write(t, c.Path, `{"hooks":`+strings.Repeat("[", 1<<20)+strings.Repeat("]", 1<<20)+`}`)
	if n := c.Check("", false); len(n) != 1 || !strings.HasPrefix(n[0], "can't check the setup in ~/.cursor/hooks.json: ") {
		t.Errorf("deep: %q", n)
	}
}
