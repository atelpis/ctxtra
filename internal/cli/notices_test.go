package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/atelpis/ctxtra/internal/agent/registry"
	"github.com/atelpis/ctxtra/internal/fsutil"
	"github.com/atelpis/ctxtra/internal/setup"
	"github.com/atelpis/ctxtra/internal/state"
	"github.com/atelpis/ctxtra/internal/testenv"
)

// noticeHome is a test home where only the agents' folders are found, and
// this binary is bin.
func noticeHome(t *testing.T) (h, bin string) {
	t.Helper()
	h = testenv.Home(t)
	t.Setenv("PATH", t.TempDir())
	bin = fakeBin(t, filepath.Join(h, "bin", "ctxtra"))
	self := bin
	running = func() string { return self }
	t.Cleanup(func() { running = setup.Running })
	return h, bin
}

func fakeBin(t *testing.T, path string) string {
	t.Helper()
	testenv.Write(t, path, "#!/bin/sh\n")
	if err := os.Chmod(path, 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func notices() []string {
	var b bytes.Buffer
	setupNotices(&b)
	return slices.DeleteFunc(strings.Split(b.String(), "\n"), func(s string) bool { return s == "" })
}

func setUp(t *testing.T, bin string, agents ...string) {
	t.Helper()
	o := setup.Options{Agents: agents}
	o.Binary = bin
	r, err := setup.Run(o)
	if err != nil || len(r.Errors) > 0 || r.Record != nil {
		t.Fatalf("setup %v: %v, %v, %v", agents, err, r.Errors, r.Record)
	}
}

func wantNotices(t *testing.T, name string, want ...string) {
	t.Helper()
	if got := notices(); !slices.Equal(got, want) {
		t.Errorf("%s: notices\n%s\nwant\n%s", name, strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

// After a fresh setup of every agent, nothing is to be done, and
// setup.json lists each agent's config.
func TestNoNoticesAfterSetup(t *testing.T) {
	h, bin := noticeHome(t)
	setUp(t, bin, registry.Names()...)
	testenv.Write(t, filepath.Join(h, ".hermes", "config.yaml"), "plugins:\n  enabled: [ctxtra]\n")
	wantNotices(t, "fresh setup")
	s, ok, err := state.LoadSetups()
	if !ok || err != nil {
		t.Fatalf("setup.json: %v, %v", ok, err)
	}
	for _, a := range registry.All() {
		if !s.Known(a.Name(), a.ConfigPath()) || !s.Seen(a.Name()) {
			t.Errorf("%s: setup.json %+v", a.Name(), s)
		}
	}
	if !slices.Contains(s.Detected, "claude") {
		t.Errorf("detected: %v", s.Detected)
	}
	// A binary in a temporary folder isn't compared.
	running = func() string { return "" }
	wantNotices(t, "go run")
}

// Each problem with a setup is one notice, which names the agent in the
// command that repairs it.
func TestNotices(t *testing.T) {
	h, bin := noticeHome(t)
	gone := filepath.Join(h, "gone", "ctxtra")
	setUp(t, gone, "claude")
	wantNotices(t, "missing binary", "claude: the hooks run ~/gone/ctxtra, which doesn't exist; run ctxtra setup --agent claude")

	other := fakeBin(t, filepath.Join(h, "other", "ctxtra"))
	setUp(t, other, "claude", "codex")
	wantNotices(t, "other binary",
		"claude: the hooks run ~/other/ctxtra, not this ctxtra (~/bin/ctxtra); to switch, run ctxtra setup --agent claude",
		"codex: the hooks run ~/other/ctxtra, not this ctxtra (~/bin/ctxtra); to switch, run ctxtra setup --agent codex (Codex will then ask you to trust the hooks again)")

	setUp(t, bin, "claude", "codex", "hermes", "dsh")
	wantNotices(t, "Hermes not enabled", "hermes: the ctxtra plugin is installed but not enabled; run hermes plugins enable ctxtra")
	testenv.Write(t, filepath.Join(h, ".hermes", "config.yaml"), "plugins:\n  enabled: [ctxtra]\n  disabled: [ctxtra]\n")
	wantNotices(t, "Hermes disabled", "hermes: the ctxtra plugin is installed but not enabled; run hermes plugins enable ctxtra")
	testenv.Write(t, filepath.Join(h, ".hermes", "config.yaml"), "plugins:\n  enabled: [ctxtra]\n")
	wantNotices(t, "Hermes enabled")

	patch := filepath.Join(h, ".dsh", "cordis.patch.yml")
	testenv.Write(t, patch, "[]\n")
	wantNotices(t, "DSH row missing", "dsh: the patch row that loads ctxtra's plugin is missing; run ctxtra setup --agent dsh")
	setUp(t, bin, "dsh")

	// Gone: setup.json lists the config, which no longer has the hooks.
	settings := filepath.Join(h, ".claude", "settings.json")
	testenv.Write(t, settings, "{}\n")
	wantNotices(t, "gone", "claude: ctxtra's hooks are gone from ~/.claude/settings.json; if that wasn't on purpose, run ctxtra setup --agent claude; "+
		"if it was, ctxtra setup --remove --agent claude stops this notice")
	o := setup.Options{Agents: []string{"claude"}}
	o.Remove = true
	if _, err := setup.Run(o); err != nil {
		t.Fatal(err)
	}
	wantNotices(t, "removed")

	// New: found since the last setup.
	for _, d := range []string{".cursor", ".pi/agent"} {
		if err := os.MkdirAll(filepath.Join(h, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	wantNotices(t, "new",
		"pi: found since the last ctxtra setup; to add ctxtra to it, run ctxtra setup --agent pi",
		"cursor: found since the last ctxtra setup; to add ctxtra to it, run ctxtra setup --agent cursor")
	setUp(t, bin, "codex")
	wantNotices(t, "seen at a setup")
}

// A new Codex is set up with a trust prompt, and the notice says so.
func TestNewCodexNotice(t *testing.T) {
	h, bin := noticeHome(t)
	setUp(t, bin, "claude")
	if err := os.MkdirAll(filepath.Join(h, ".codex"), 0o755); err != nil {
		t.Fatal(err)
	}
	wantNotices(t, "new Codex", "codex: found since the last ctxtra setup; to add ctxtra to it, run ctxtra setup --agent codex (Codex will then ask you to trust the hooks)")
}

// Without setup.json there are no notices about new or gone setups; a
// broken one says so once, and is then as if missing.
func TestSetupsFile(t *testing.T) {
	h, bin := noticeHome(t)
	setUp(t, bin, "claude")
	testenv.Write(t, filepath.Join(h, ".claude", "settings.json"), "{}\n")
	if err := os.MkdirAll(filepath.Join(h, ".cursor"), 0o755); err != nil {
		t.Fatal(err)
	}
	if len(notices()) != 2 {
		t.Fatalf("notices %q", notices())
	}
	unreadable := "~/.ctxtra/setup.json is unreadable: notices about new or removed setups are off until the next ctxtra setup"
	for name, content := range map[string]string{
		"broken":     `{"installed": [`,
		"wrong type": `{"installed": {"claude": "x"}}`,
		"huge":       `{"detected": ["` + strings.Repeat("x", 2<<20) + `"]}`,
	} {
		testenv.Write(t, state.SetupsPath(), content)
		wantNotices(t, name, unreadable)
	}
	os.Remove(state.SetupsPath())
	wantNotices(t, "missing")
	// A list that names another config isn't this one's setup.
	testenv.Write(t, state.SetupsPath(), `{"installed": {"claude": ["/elsewhere/settings.json"]}, "detected": ["cursor"]}`)
	wantNotices(t, "another config")
}

// setup.json lists an agent only after its setup worked, until a removal
// worked. --dry-run doesn't write it.
func TestSetupRecord(t *testing.T) {
	_, bin := noticeHome(t)
	binary = func() (string, []string, error) { return bin, nil, nil }
	t.Cleanup(func() { binary = setup.Binary })
	installed := func() map[string][]string {
		t.Helper()
		s, _, err := state.LoadSetups()
		if err != nil {
			t.Fatal(err)
		}
		return s.Installed
	}
	claude, codex := registry.All()[0], registry.All()[1]
	if code, _, _ := run("setup", "--dry-run", "--agent", "claude"); code != 0 {
		t.Fatalf("dry run: %d", code)
	}
	if _, err := os.Stat(state.SetupsPath()); !os.IsNotExist(err) {
		t.Errorf("dry run wrote setup.json: %v", err)
	}
	testenv.Write(t, codex.ConfigPath(), `{"hooks": []}`)
	if code, _, errOut := run("setup", "--agent", "claude", "--agent", "codex"); code != 1 || !strings.Contains(errOut, "codex: ") {
		t.Fatalf("partial failure: %d %s", code, errOut)
	}
	if got := installed(); len(got) != 1 || !slices.Equal(got["claude"], []string{claude.ConfigPath()}) {
		t.Errorf("after a partial failure: %v", got)
	}
	if code, _, _ := run("setup", "--remove", "--agent", "claude"); code != 0 {
		t.Fatal(code)
	}
	if got := installed(); len(got) != 0 {
		t.Errorf("after --remove: %v", got)
	}

	// setup.json can't be written: the config changes stay, setup fails.
	os.Remove(state.SetupsPath())
	if err := os.MkdirAll(filepath.Join(state.SetupsPath(), "x"), 0o755); err != nil {
		t.Fatal(err)
	}
	code, out, errOut := run("setup", "--agent", "claude")
	if code != 1 || !strings.Contains(out, "changed") || !strings.Contains(errOut, "ctxtra setup: can't write ~/.ctxtra/setup.json: ") {
		t.Errorf("unwritable setup.json: %d %q %q", code, out, errOut)
	}
	if b, _ := os.ReadFile(claude.ConfigPath()); !strings.Contains(string(b), "hook claude SessionStart") {
		t.Errorf("config not changed:\n%s", b)
	}
}

// The notices follow every command but hook, setup, doctor, help and
// version, and never change the exit code. doctor shows them under each
// agent.
func TestNoticesFollowCommands(t *testing.T) {
	h, _ := noticeHome(t)
	setUp(t, filepath.Join(h, "gone", "ctxtra"), "claude")
	notice := "claude: the hooks run ~/gone/ctxtra, which doesn't exist; run ctxtra setup --agent claude\n"
	for _, args := range [][]string{{"version"}, {"help"}, {"--help"}, {"hook", "claude", "SessionStart"}, {"nonsense"}} {
		if _, _, errOut := run(args...); strings.Contains(errOut, notice) {
			t.Errorf("%v: %q", args, errOut)
		}
	}
	if code, _, errOut := run("files", h); code != 0 || errOut != notice {
		t.Errorf("files: %d %q", code, errOut)
	}
	if code, out, errOut := run("doctor", h); code != 1 || !strings.Contains(out, "\n    the hooks run ~/gone/ctxtra, which doesn't exist") || strings.Contains(errOut, notice) {
		t.Errorf("doctor: %d %q %q", code, out, errOut)
	}
}

// Check reads files only: the notices don't run the codex on PATH, which
// doctor does.
func TestNoticesRunNothing(t *testing.T) {
	h, bin := noticeHome(t)
	setUp(t, bin, "codex")
	path := t.TempDir()
	ran := filepath.Join(h, "ran")
	testenv.Write(t, filepath.Join(path, "codex"), "#!/bin/sh\n/usr/bin/touch "+ran+"\n")
	if err := os.Chmod(filepath.Join(path, "codex"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", path)
	wantNotices(t, "codex in PATH")
	if _, err := os.Stat(ran); !os.IsNotExist(err) {
		t.Errorf("the notices ran codex: %v", err)
	}
	run("doctor", h)
	if _, err := os.Stat(ran); err != nil {
		t.Errorf("doctor didn't run codex, so the test shows nothing: %v", err)
	}
}

// A new agent is told about once: the notice adds it to detected in
// setup.json, without waiting for a setup that holds the lock, and then
// shows again. Without a new agent, setup.json isn't written.
func TestNewAgentNoticeOnce(t *testing.T) {
	h, bin := noticeHome(t)
	setUp(t, bin, "claude")
	before, _ := os.Stat(state.SetupsPath())
	wantNotices(t, "nothing new")
	if after, _ := os.Stat(state.SetupsPath()); !os.SameFile(before, after) || !after.ModTime().Equal(before.ModTime()) {
		t.Error("setup.json written without a notice")
	}
	if err := os.MkdirAll(filepath.Join(h, ".cursor"), 0o755); err != nil {
		t.Fatal(err)
	}
	notice := "cursor: found since the last ctxtra setup; to add ctxtra to it, run ctxtra setup --agent cursor"
	unlock, err := fsutil.Lock(state.SetupsLock())
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	wantNotices(t, "locked", notice)
	wantNotices(t, "still locked", notice)
	if d := time.Since(start); d > time.Second {
		t.Errorf("the notices waited %s for the lock", d)
	}
	unlock()
	wantNotices(t, "new", notice)
	wantNotices(t, "told")
	s, _, _ := state.LoadSetups()
	if !slices.Contains(s.Detected, "cursor") || len(s.Installed) != 1 || !s.Known("claude", filepath.Join(h, ".claude", "settings.json")) {
		t.Errorf("setup.json %+v", s)
	}
	// doctor still lists it as not set up.
	if _, out, _ := run("doctor", h); !strings.Contains(out, "  cursor: not set up  ") {
		t.Errorf("doctor:\n%s", out)
	}
}

// A notice that records a new agent while a setup runs leaves setup.json
// whole, with the setup's change.
func TestNewAgentNoticeDuringSetup(t *testing.T) {
	h, bin := noticeHome(t)
	for i := range 20 {
		os.Remove(state.SetupsPath())
		os.RemoveAll(filepath.Join(h, ".cursor"))
		setUp(t, bin, "claude")
		o := setup.Options{Agents: []string{"codex"}}
		o.Binary = bin
		if i%2 == 1 {
			o.Remove = true
		}
		if err := os.MkdirAll(filepath.Join(h, ".cursor"), 0o755); err != nil {
			t.Fatal(err)
		}
		done := make(chan error)
		go func() {
			r, err := setup.Run(o)
			if err == nil {
				err = r.Record
			}
			done <- err
		}()
		notices()
		if err := <-done; err != nil {
			t.Fatal(err)
		}
		s, ok, err := state.LoadSetups()
		codex := filepath.Join(h, ".codex", "hooks.json")
		if !ok || err != nil || !s.Known("claude", filepath.Join(h, ".claude", "settings.json")) || s.Known("codex", codex) == o.Remove ||
			!slices.Contains(s.Detected, "cursor") {
			t.Fatalf("run %d: %+v, %v, %v", i, s, ok, err)
		}
	}
}
