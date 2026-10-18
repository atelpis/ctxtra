package codex

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/atelpis/ctxtra/internal/agent"
	"github.com/atelpis/ctxtra/internal/testenv"
)

func TestTrustRecords(t *testing.T) {
	h := testenv.Home(t)
	p := filepath.Join(h, "config.toml")
	testenv.Write(t, p, `[hooks.state."/h/hooks.json:session_start:1:0"]
enabled = true
trusted_hash = "sha256:x"

[hooks.state."/h/hooks.json:subagent_start:1:0"] # a comment
enabled = true
# trusted_hash = "sha256:commented out"

[hooks.state."/h/hooks.json:stop:0:0"]
trusted_hash_v2 = "not this key"
`)
	if got := trustRecords(p); !slices.Equal(got, []string{"/h/hooks.json:session_start:1:0"}) {
		t.Errorf("records %v", got)
	}
}

// Setup asks for a trust review whenever it changed a Codex hook, also when
// an old trust record exists: Codex doesn't run a changed hook.
func TestSetupAndStatus(t *testing.T) {
	h := testenv.Home(t)
	bin := filepath.Join(h, "bin", "ctxtra")
	testenv.Write(t, bin, "#!/bin/sh\n")
	if err := os.Chmod(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	orig := version
	version = func() string { return "" }
	t.Cleanup(func() { version = orig })
	var a Agent
	var r agent.Report
	if err := a.Setup(agent.Options{Binary: bin}, &r); err != nil || len(r.Notes) != 1 {
		t.Fatalf("setup: %v, notes %v", err, r.Notes)
	}
	st := a.Status()
	if st.OK || len(st.Details) != 2 || !strings.Contains(st.Details[0], "NOT TRUSTED") {
		t.Errorf("status %+v", st)
	}
	key := a.ConfigPath() + ":session_start:0:0"
	testenv.Write(t, filepath.Join(h, ".codex", "config.toml"), "[hooks.state.\""+key+"\"]\ntrusted_hash = \"x\"\n")
	if st := a.Status(); !strings.Contains(st.Details[0], "trust record present") || !strings.Contains(st.Details[1], "NOT TRUSTED") {
		t.Errorf("status %+v", st)
	}
	r = agent.Report{}
	if err := a.Setup(agent.Options{Binary: bin}, &r); err != nil || len(r.Notes) != 0 {
		t.Errorf("unchanged setup: %v, notes %v", err, r.Notes)
	}
	if err := a.Setup(agent.Options{Binary: filepath.Join(h, "other", "ctxtra")}, &r); err != nil || len(r.Notes) != 1 {
		t.Errorf("changed setup: %v, notes %v", err, r.Notes)
	}
}

func TestVersion(t *testing.T) {
	testenv.Home(t)
	orig := version
	t.Cleanup(func() { version = orig })
	for v, warn := range map[string]bool{"0.144.3": true, "0.99.0": true, "0.145.0": false, "0.162.1": false, "1.0.0": false} {
		version = func() string { return v }
		st := Agent{}.Status()
		if got := slices.ContainsFunc(st.Details, func(d string) bool { return strings.Contains(d, "update to 0.145.0") }); got != warn {
			t.Errorf("%s: warning %v, want %v: %v", v, got, warn, st.Details)
		}
	}
}

// The hook takes the old limit unless hooks.json shows ctxtra's hooks for
// the event with additionalContextLimit, as setup writes them, also in
// $CODEX_HOME.
func TestLegacy(t *testing.T) {
	h := testenv.Home(t)
	t.Setenv("CODEX_HOME", filepath.Join(h, "codex-home"))
	bin := filepath.Join(h, "bin", "ctxtra")
	var a Agent
	if _, old := a.Legacy("SessionStart", agent.Input{}); !old {
		t.Error("without hooks.json: not old")
	}
	if err := a.Setup(agent.Options{Binary: bin}, &agent.Report{}); err != nil {
		t.Fatal(err)
	}
	for _, ev := range []string{"SessionStart", "SubagentStart"} {
		if _, old := a.Legacy(ev, agent.Input{}); old {
			t.Errorf("%s as set up: old", ev)
		}
	}
	// A command ctxtra doesn't write, with the limit: not recognized.
	testenv.Write(t, a.ConfigPath(), `{"hooks": {"SessionStart": [{"hooks": [{"type": "command", `+
		`"command": "exec ctxtra hook codex SessionStart", "additionalContextLimit": 250000}]}]}}`)
	if d, old := a.Legacy("SessionStart", agent.Input{}); !old || d.Capacity != 9000 {
		t.Errorf("hand-written hook: %v, %+v", old, d)
	}
}
