package hook

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/atelpis/ctxtra/internal/testenv"
)

func TestOutputPerAgent(t *testing.T) {
	h := testenv.Home(t)
	r := testenv.Repo(t, filepath.Join(h, "svc"), "")
	testenv.Write(t, filepath.Join(h, "a.md"), "Rule A <&>")
	testenv.Skill(t, filepath.Join(h, "s", "deploy"), "description: Deploy it.")
	good := "apiVersion: ctxtra/v1alpha1\nkind: Config\nbindings: [b.yaml]\n"
	testenv.Write(t, filepath.Join(h, ".config", "ctxtra", "b.yaml"),
		"apiVersion: ctxtra/v1alpha1\nkind: Binding\nmetadata: {name: a}\nspec: {selector: {global: true}, files: [~/a.md], skills: [~/s/deploy]}\n")
	testenv.Config(t, h, good)

	run := func(agent, event, input string) map[string]any {
		t.Helper()
		var out, errOut bytes.Buffer
		if code := Run(agent, event, strings.NewReader(input), &out, &errOut); code != 0 {
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
	if m := run("claude", "SessionStart", input); m["systemMessage"] != "ctxtra: added 1 file (a.md) and 1 skill (deploy)" {
		t.Errorf("summary: %v", m["systemMessage"])
	}
	if m := run("claude", "SubagentStart", input); m["systemMessage"] != nil || m["hookSpecificOutput"] == nil {
		t.Errorf("subagent: %v", m)
	}
	// A broken config adds nothing, tells the user, and still answers with
	// valid JSON.
	testenv.Config(t, h, "apiVersion: [")
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
	if err != nil || strings.Count(string(b), "\n") != 11 {
		t.Errorf("log: %v %q", err, b)
	}
	// The malformed and unknown runs have no folder; the others are 8
	// different agent and event pairs in this repository.
	if runs := LastRuns(r, r); len(runs) != 8 || runs[0].Harness != "antigravity" {
		t.Errorf("last runs: %+v", runs)
	}
}
