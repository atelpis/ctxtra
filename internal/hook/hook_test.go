package hook

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/atelpis/ctxtra/internal/agent"
	"github.com/atelpis/ctxtra/internal/agent/registry"
	"github.com/atelpis/ctxtra/internal/paths"
	"github.com/atelpis/ctxtra/internal/testenv"
)

func TestOutputPerAgent(t *testing.T) {
	h := testenv.Home(t)
	r := testenv.Repo(t, filepath.Join(h, "svc"), "")
	testenv.Write(t, filepath.Join(h, "a.md"), "Rule A <&>")
	testenv.Skill(t, filepath.Join(h, "s", "deploy"), "description: Deploy it.")
	good := "schema: ctxtra/v1\ninclude: [b.yaml]\n"
	testenv.Write(t, filepath.Join(h, ".config", "ctxtra", "b.yaml"),
		"schema: ctxtra/v1\nbindings:\n  - {name: a, match: {global: true}, instructions: [~/a.md], skills: [~/s/deploy]}\n")
	testenv.Config(t, h, good)

	// As set up: Claude's hooks have slots, which other agents ignore.
	run := func(agent, event, input string) map[string]any {
		t.Helper()
		var out, errOut bytes.Buffer
		if code := RunSlot(agent, event, 0, strings.NewReader(input), &out, &errOut); code != 0 {
			t.Fatalf("%s: exit %d", agent, code)
		}
		var m map[string]any
		if err := json.Unmarshal(out.Bytes(), &m); err != nil {
			t.Fatalf("%s: bad JSON %q", agent, out.String())
		}
		return m
	}
	input := `{"cwd": "` + r + `"}`
	c := run("claude", "SubagentStart", input)["hookSpecificOutput"].(map[string]any)
	if c["hookEventName"] != "SubagentStart" || !strings.Contains(c["additionalContext"].(string), "Rule A <&>") ||
		!strings.Contains(c["additionalContext"].(string), "<name>deploy</name>") {
		t.Errorf("claude: %v", c)
	}
	if cu := run("cursor", "sessionStart", `{"workspace_roots": ["`+r+`"]}`); !strings.Contains(cu["additional_context"].(string), "Rule A") {
		t.Errorf("cursor: %v", cu)
	}
	if co := run("copilot", "sessionStart", input); !strings.Contains(co["additionalContext"].(string), "Rule A") {
		t.Errorf("copilot: %v", co)
	}
	if d := run("devin", "SessionStart", input)["hookSpecificOutput"].(map[string]any); d["hookEventName"] != "SessionStart" ||
		!strings.Contains(d["additionalContext"].(string), "Rule A") {
		t.Errorf("devin: %v", d)
	}
	pi := run("pi", "before_agent_start", input)
	if f, s := pi["files"].([]any), pi["skills"].([]any); len(f) != 1 || len(s) != 1 || s[0] != filepath.Join(h, "s", "deploy", "SKILL.md") {
		t.Errorf("pi: %v", pi)
	}
	oc := run("opencode", "context", input)
	if s := oc["skills"].([]any); !strings.Contains(oc["text"].(string), "Rule A <&>") || strings.Contains(oc["text"].(string), "<name>deploy</name>") ||
		len(s) != 1 || s[0].(map[string]any)["name"] != "deploy" {
		t.Errorf("opencode: %v", oc)
	}
	// Antigravity sends no cwd and runs the hook in another folder.
	ag := run("antigravity", "SessionStart", `{"workspacePaths": ["`+r+`"], "conversationId": "c1"}`)
	if steps := ag["injectSteps"].([]any); len(steps) != 1 ||
		!strings.Contains(steps[0].(map[string]any)["systemMessage"].(map[string]any)["systemMessage"].(string), "Rule A <&>") {
		t.Errorf("antigravity: %v", ag)
	}
	// The user sees a summary at session start, not for subagents.
	if m := run("claude", "SessionStart", input); m["systemMessage"] != "✓ ctxtra added 1 instruction and 1 skill\nInstructions:\n- a.md\nSkills:\n- deploy" {
		t.Errorf("summary: %v", m["systemMessage"])
	}
	if m := run("claude", "SubagentStart", input); m["systemMessage"] != nil || m["hookSpecificOutput"] == nil {
		t.Errorf("subagent: %v", m)
	}
	// A broken config adds nothing, tells the user, and still answers with
	// valid JSON.
	testenv.Config(t, h, "schema: [")
	if m := run("codex", "SessionStart", input); len(m) != 1 || !strings.Contains(m["systemMessage"].(string), "1 problem (run ctxtra doctor)") {
		t.Errorf("broken config: %v", m)
	}
	// Malformed input adds nothing, even with a working config.
	testenv.Config(t, h, good)
	if m := run("claude", "SessionStart", `{"cwd": true}`); len(m) != 0 {
		t.Errorf("malformed input: %v", m)
	}
	// An unknown agent gets an empty answer.
	if m := run("vim", "start", input); len(m) != 0 {
		t.Errorf("unknown agent: %v", m)
	}
	b, err := os.ReadFile(filepath.Join(h, ".ctxtra", "log.jsonl"))
	if err != nil || strings.Count(string(b), "\n") != 12 {
		t.Errorf("log: %v %q", err, b)
	}
	// The malformed and unknown runs have no folder; the others are 9
	// different agent and event pairs in this repository.
	if runs := LastRuns(r, r); len(runs) != 9 || runs[0].Harness != "antigravity" {
		t.Errorf("last runs: %+v", runs)
	}
}

// Devin also runs Claude's hooks. With ctxtra's Devin hook set up, Claude's
// hook adds nothing there, and logs why; in Claude Code it works as usual.
func TestClaudeHookUnderDevin(t *testing.T) {
	h := testenv.Home(t)
	r := testenv.Repo(t, filepath.Join(h, "svc"), "")
	testenv.Write(t, filepath.Join(h, "a.md"), "Rule A")
	testenv.Write(t, filepath.Join(h, ".config", "ctxtra", "b.yaml"),
		"schema: ctxtra/v1\nbindings:\n  - {name: a, match: {global: true}, instructions: [~/a.md]}\n")
	testenv.Config(t, h, "schema: ctxtra/v1\ninclude: [b.yaml]\n")
	cmd, _ := json.Marshal(agent.Command("/bin/ctxtra", "devin", "SessionStart", nil))
	testenv.Write(t, filepath.Join(h, ".config", "devin", "config.json"),
		`{"hooks": {"SessionStart": [{"hooks": [{"type": "command", "command": `+string(cmd)+`}]}]}}`)
	run := func() string {
		var out bytes.Buffer
		Run("claude", "SessionStart", strings.NewReader(`{"cwd": "`+r+`"}`), &out, io.Discard)
		return out.String()
	}
	if out := run(); !strings.Contains(out, "Rule A") {
		t.Errorf("claude: %s", out)
	}
	t.Setenv("DEVIN_PROJECT_DIR", r)
	if out := run(); strings.TrimSpace(out) != "{}" {
		t.Errorf("claude under devin: %s", out)
	}
	if runs := LastRuns(r, r); len(runs) != 1 || !strings.HasPrefix(runs[0].Skipped, "run by Devin") || runs[0].Chars != 0 {
		t.Errorf("last runs: %+v", runs)
	}
	// Without ctxtra's Devin hook, Claude's hook is the one that serves Devin.
	testenv.Write(t, filepath.Join(h, ".config", "devin", "config.json"), `{}`)
	if out := run(); !strings.Contains(out, "Rule A") {
		t.Errorf("claude under devin, no devin hook: %s", out)
	}
}

// The Cursor desktop app runs Claude's hooks too, with cursor_version in the
// input. With ctxtra's Cursor hook set up, every slot of Claude's hook adds
// nothing, and only slot 0 logs why.
func TestClaudeHookUnderCursor(t *testing.T) {
	h := testenv.Home(t)
	r := testenv.Repo(t, filepath.Join(h, "svc"), "")
	testenv.Write(t, filepath.Join(h, "a.md"), "Rule A")
	testenv.Write(t, filepath.Join(h, ".config", "ctxtra", "b.yaml"),
		"schema: ctxtra/v1\nbindings:\n  - {name: a, match: {global: true}, instructions: [~/a.md]}\n")
	testenv.Config(t, h, "schema: ctxtra/v1\ninclude: [b.yaml]\n")
	cmd, _ := json.Marshal(agent.Command("/bin/ctxtra", "cursor", "sessionStart", nil))
	testenv.Write(t, filepath.Join(h, ".cursor", "hooks.json"),
		`{"version": 1, "hooks": {"sessionStart": [{"command": `+string(cmd)+`}]}}`)
	run := func(slot int, input string) string {
		var out bytes.Buffer
		RunSlot("claude", "SessionStart", slot, strings.NewReader(input), &out, io.Discard)
		return out.String()
	}
	fromCursor := `{"cwd": "` + r + `", "session_id": "s1", "hook_event_name": "sessionStart", "cursor_version": "3.25.9"}`
	for _, slot := range []int{0, 1, 39} {
		if out := run(slot, fromCursor); strings.TrimSpace(out) != "{}" {
			t.Errorf("claude slot %d under cursor: %s", slot, out)
		}
	}
	if runs := LastRuns(r, r); len(runs) != 1 || !strings.HasPrefix(runs[0].Skipped, "run by Cursor") {
		t.Errorf("last runs: %+v", runs)
	}
	if b, _ := os.ReadFile(paths.LogPath()); strings.Count(string(b), "run by Cursor") != 1 {
		t.Errorf("log entries for the skipped slots, want 1:\n%s", b)
	}
	// Claude Code itself sends no cursor_version.
	if out := run(0, `{"cwd": "`+r+`", "session_id": "s2"}`); !strings.Contains(out, "Rule A") {
		t.Errorf("claude: %s", out)
	}
	// Without ctxtra's Cursor hook, Claude's hook is the one that serves Cursor.
	testenv.Write(t, filepath.Join(h, ".cursor", "hooks.json"), `{"version": 1, "hooks": {}}`)
	if out := run(-1, fromCursor); !strings.Contains(out, "Rule A") {
		t.Errorf("claude under cursor, no cursor hook: %s", out)
	}
}

// Hooks that an older version set up, which call this version: they get the
// text they got then, whole files dropped from the end, and the user and
// the log are told to run setup.
func TestOldHooks(t *testing.T) {
	h := testenv.Home(t)
	r := testenv.Repo(t, filepath.Join(h, "svc"), "")
	testenv.Write(t, filepath.Join(h, "small.md"), "SMALL FILE")
	testenv.Write(t, filepath.Join(h, "big.md"), strings.Repeat("big \"line\" with \\ escapes\n", 1200))
	testenv.Write(t, filepath.Join(h, ".config", "ctxtra", "b.yaml"),
		"schema: ctxtra/v1\nbindings:\n  - {name: a, match: {global: true}, instructions: [~/small.md, ~/big.md]}\n")
	testenv.Config(t, h, "schema: ctxtra/v1\ninclude: [b.yaml]\n")
	// Codex's hooks as the previous version wrote them: no
	// additionalContextLimit.
	cmd, _ := json.Marshal(agent.Command("/bin/ctxtra", "codex", "SessionStart", nil))
	codexHooks := `{"hooks": {"SessionStart": [{"matcher": "startup|clear|compact", "hooks": [{"type": "command", "command": ` +
		string(cmd) + `, "timeout": 10, "statusMessage": "ctxtra: personal instructions"}]}]}}`
	testenv.Write(t, filepath.Join(h, ".codex", "hooks.json"), codexHooks)
	run := func(harness string, slot int, input string) string {
		var out bytes.Buffer
		RunSlot(harness, "SessionStart", slot, strings.NewReader(input), &out, io.Discard)
		return out.String()
	}
	input := `{"cwd": "` + r + `"}`
	for _, c := range []struct {
		harness, input, limit string
		slot                  int
	}{
		{"claude", input, "Claude Code's limit of 9,000 characters", -1},
		{"muse", input, "Muse's limit of 13,000 bytes", -1},
		{"codex", input, "Codex's limit of 9,000 bytes", -1},
		{"hermes", input, "Hermes's limit of 9,000 characters", -1},
	} {
		out := run(c.harness, c.slot, c.input)
		if !strings.Contains(out, "SMALL FILE") || strings.Contains(out, "big \\\"line") {
			t.Errorf("%s: %.300s", c.harness, out)
		}
		if c.harness != "hermes" && (!strings.Contains(out, "dropped big.md (over "+c.limit+")") || !strings.Contains(out, "run ctxtra setup")) {
			t.Errorf("%s: user message in %.600s", c.harness, out)
		}
		e := logEntries(t, h)
		last := e[len(e)-1]
		if len(last.Dropped) != 1 || !slices.ContainsFunc(last.Problems, func(p string) bool { return strings.Contains(p, "run ctxtra setup") }) {
			t.Errorf("%s: log %+v", c.harness, last)
		}
	}
	// Hooks of this version get it all.
	limited := strings.Replace(codexHooks, `"timeout": 10,`, `"timeout": 10, "additionalContextLimit": 250000,`, 1)
	testenv.Write(t, filepath.Join(h, ".codex", "hooks.json"), limited)
	for harness, input := range map[string]string{"codex": input, "hermes": `{"cwd": "` + r + `", "ctxtra_plugin": 2}`} {
		if out := run(harness, -1, input); !strings.Contains(out, "big \\\"line") || strings.Contains(out, "run ctxtra setup") {
			t.Errorf("%s of this version: %.300s", harness, out)
		}
	}
	if out := run("claude", 0, input); !strings.Contains(out, "big \\\"line") || strings.Contains(out, "run ctxtra setup") {
		t.Errorf("claude slot 0: %.300s", out)
	}
}

// Options that can't be read add nothing, and are logged.
func TestBadArgs(t *testing.T) {
	h := testenv.Home(t)
	var out bytes.Buffer
	RunArgs("claude", "SessionStart", Args{Slot: -1, Err: errors.New("--slot given twice")}, strings.NewReader(`{"cwd": "/x"}`), &out, io.Discard)
	if strings.TrimSpace(out.String()) != "{}" {
		t.Errorf("answer %q", out.String())
	}
	if e := logEntries(t, h); len(e) != 1 || e[0].Error != "bad hook arguments: --slot given twice" {
		t.Errorf("log %+v", e)
	}
}

// Hermes's plugin says how many callbacks it registered: the text is
// fitted to them. A count that isn't a positive number adds nothing.
func TestHermesSlots(t *testing.T) {
	h := testenv.Home(t)
	r := testenv.Repo(t, filepath.Join(h, "svc"), "")
	testenv.Write(t, filepath.Join(h, "big.md"), strings.Repeat("big line of text\n", 2000))
	testenv.Write(t, filepath.Join(h, ".config", "ctxtra", "b.yaml"),
		"schema: ctxtra/v1\nbindings:\n  - {name: a, match: {global: true}, instructions: [~/big.md]}\n")
	testenv.Config(t, h, "schema: ctxtra/v1\ninclude: [b.yaml]\n")
	run := func(extra string) map[string]any {
		t.Helper()
		var out bytes.Buffer
		Run("hermes", "pre_llm_call", strings.NewReader(`{"cwd": "`+r+`"`+extra+`}`), &out, io.Discard)
		var m map[string]any
		if err := json.Unmarshal(out.Bytes(), &m); err != nil {
			t.Fatalf("%s: %q", extra, out.String())
		}
		return m
	}
	for extra, parts := range map[string]int{
		`, "ctxtra_plugin": 2`:                      4,
		`, "ctxtra_plugin": 3, "ctxtra_slots": 125`: 4,
		`, "ctxtra_plugin": 3, "ctxtra_slots": 2`:   0,
	} {
		if got, _ := run(extra)["chunks"].([]any); len(got) != parts {
			t.Errorf("%s: %d parts, want %d", extra, len(got), parts)
		}
	}
	for _, extra := range []string{`, "ctxtra_plugin": 3, "ctxtra_slots": 0`, `, "ctxtra_plugin": 3, "ctxtra_slots": -2`,
		`, "ctxtra_plugin": 3, "ctxtra_slots": "x"`, `, "ctxtra_plugin": 3, "ctxtra_slots": null`, `, "ctxtra_plugin": 3, "ctxtra_slots": 1.5`} {
		if m := run(extra); len(m) != 0 {
			t.Errorf("%s: %v", extra, m)
		}
		if e := logEntries(t, h); !strings.HasPrefix(e[len(e)-1].Error, "bad hook input: ") {
			t.Errorf("%s: log %+v", extra, e[len(e)-1])
		}
	}
}

// more is an agent whose Delivery has more slots than its setups from
// before version 1, as after a later change of the slot count.
type more struct {
	agent.Agent
	slots int
}

func (m more) Delivery() agent.Delivery {
	d := m.Agent.Delivery()
	d.Capacity, d.Slots = m.slots*d.Chunk, m.slots
	return d
}

func (m more) OldSlots() int { return m.Agent.(agent.OldSlots).OldSlots() }

func (m more) Legacy(event string, in agent.Input) (agent.Delivery, bool) {
	return m.Agent.(agent.Upgrader).Legacy(event, in)
}

// A setup that doesn't say how many slots it has gets the count that
// setups from before version 1 have, not the agent's current count.
func TestOldSlots(t *testing.T) {
	testenv.Home(t)
	for name, old := range map[string]int{"claude": 40, "muse": 40, "hermes": 125} {
		a, _ := registry.Get(name)
		m := more{a, old * 2}
		in := agent.Input{Slot: 3, Plugin: 2}
		if d, _ := delivery(m, "SessionStart", in); d.Slots != old {
			t.Errorf("%s without --of: %d slots, want %d", name, d.Slots, old)
		}
		in.Slots = old * 2
		if d, _ := delivery(m, "SessionStart", in); d.Slots != old*2 {
			t.Errorf("%s with --of %d: %d slots", name, old*2, d.Slots)
		}
	}
}
