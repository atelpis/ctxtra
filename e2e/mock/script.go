package mock

import (
	"fmt"
	"regexp"
	"strings"
)

// Step is one scripted action of the main agent.
type Step struct {
	kind string // "read", "skill", "subagent", "wait"
	path string
	name string // skill
}

// Read makes the agent read the file at path (an absolute path).
func Read(path string) Step { return Step{kind: "read", path: path} }

// LoadSkill makes the agent load the skill name with its skill tool, or, for
// agents without one, read its SKILL.md at path.
func LoadSkill(name, path string) Step { return Step{kind: "skill", name: name, path: path} }

// StartSubagent makes the agent start a subagent with SubagentTask. In Codex
// and Muse, where subagents run in the background, it then also waits for it.
func StartSubagent() Step { return Step{kind: "subagent"} }

func (s Step) String() string {
	switch s.kind {
	case "read":
		return "read " + s.path
	case "skill":
		return "skill " + s.name
	}
	return s.kind
}

// call is a tool call the mock answers with.
type call struct {
	namespace string // Codex groups some tools, e.g. multi-agent tools
	name      string
	args      map[string]any
}

// decide picks the next step: the script position is the number of tool
// results the request already carries, so the mock keeps no state. A step
// whose tool the request doesn't offer gets a text answer.
func decide(api API, body map[string]any, script []Step) *call {
	var steps []Step
	for _, s := range script {
		steps = append(steps, s)
		if s.kind == "subagent" && api == Responses {
			steps = append(steps, Step{kind: "wait"})
		}
	}
	results := toolResults(api, body)
	if len(results) >= len(steps) {
		return nil
	}
	step := steps[len(results)]
	if step.kind == "skill" {
		// OpenCode's skill tool takes an id. Copilot's takes a name, but
		// loads only its own skills: there the agent reads SKILL.md.
		if _, schema, ok := findTool(api, body, "skill"); ok && hasParam(schema, "id") {
			return &call{name: "skill", args: map[string]any{"id": step.name}}
		}
		step.kind = "read"
	}
	switch step.kind {
	case "read":
		for _, c := range []call{
			{name: "Read", args: map[string]any{"file_path": step.path}},                            // Claude
			{name: "read", args: map[string]any{"path": step.path}},                                 // Pi, OpenCode
			{name: "view", args: map[string]any{"path": step.path}},                                 // Copilot
			{name: "exec_command", args: map[string]any{"cmd": fmt.Sprintf("cat '%s'", step.path)}}, // Codex
			{name: "read_file", args: map[string]any{"path": step.path}},                            // Muse
			{name: "view_file", args: map[string]any{"AbsolutePath": step.path, // Antigravity
				"toolAction": "Reading file", "toolSummary": "File read"}},
		} {
			if ns, _, ok := findTool(api, body, c.name); ok {
				c.namespace = ns
				return &c
			}
		}
	case "subagent":
		if _, _, ok := findTool(api, body, "Agent"); ok {
			return &call{name: "Agent", args: map[string]any{"description": "e2e probe", "prompt": SubagentTask,
				"subagent_type": "general-purpose"}}
		}
		if _, _, ok := findTool(api, body, "subagent"); ok { // OpenCode
			return &call{name: "subagent", args: map[string]any{"agent": "general", "description": "e2e probe", "prompt": SubagentTask}}
		}
		if _, _, ok := findTool(api, body, "task"); ok { // Copilot
			return &call{name: "task", args: map[string]any{"name": "e2e-probe", "description": "e2e probe",
				"prompt": SubagentTask, "agent_type": "explore", "mode": "sync"}}
		}
		if _, _, ok := findTool(api, body, "invoke_subagent"); ok { // Antigravity; runs in the background
			return &call{name: "invoke_subagent", args: map[string]any{"toolAction": "Starting subagent", "toolSummary": "E2e probe",
				"Subagents": []any{map[string]any{"TypeName": "self", "Role": "E2E Probe", "Prompt": SubagentTask}}}}
		}
		if ns, _, ok := findTool(api, body, "spawn_agent"); ok {
			return &call{namespace: ns, name: "spawn_agent", args: map[string]any{"message": SubagentTask}}
		}
		if ns, _, ok := findTool(api, body, "subagent_spawn"); ok { // Muse
			return &call{namespace: ns, name: "subagent_spawn", args: map[string]any{"command_id": "e2e-spawn",
				"role": "probe", "objective": SubagentTask}}
		}
	case "wait":
		if ns, _, ok := findTool(api, body, "subagent_wait"); ok { // Muse
			args := map[string]any{"command_id": "e2e-wait", "wait_for": "result_ready", "timeout_ms": 60000}
			if ids := uuidRe.FindAllString(results[len(results)-1], -1); len(ids) > 0 {
				args["subagent_id"] = ids[0]
			}
			return &call{namespace: ns, name: "subagent_wait", args: args}
		}
		ns, schema, ok := findTool(api, body, "wait_agent")
		if !ok {
			return nil
		}
		ids := uuidRe.FindAllString(results[len(results)-1], -1)
		args := map[string]any{}
		props, _ := schema["properties"].(map[string]any)
		for k, v := range props {
			p, _ := v.(map[string]any)
			switch t := str(p["type"]); {
			case t == "array":
				args[k] = ids[:min(1, len(ids))]
			case t == "string" && (k == "id" || k == "target" || k == "agent_id"):
				if len(ids) > 0 {
					args[k] = ids[0]
				}
			case (t == "integer" || t == "number") && strings.Contains(k, "timeout"):
				args[k] = 60000
			}
		}
		return &call{namespace: ns, name: "wait_agent", args: args}
	}
	return nil
}

// hasParam reports whether a tool's parameter schema has the property.
func hasParam(schema map[string]any, name string) bool {
	props, _ := schema["properties"].(map[string]any)
	_, ok := props[name]
	return ok
}

var uuidRe = regexp.MustCompile(`[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}`)

// findTool returns the namespace and parameter schema of a tool the request
// offers.
func findTool(api API, body map[string]any, name string) (ns string, schema map[string]any, ok bool) {
	tools, _ := body["tools"].([]any)
	for _, t := range tools {
		tm, _ := t.(map[string]any)
		if api == Gemini {
			decls, _ := tm["functionDeclarations"].([]any)
			for _, d := range decls {
				dm, _ := d.(map[string]any)
				if str(dm["name"]) == name {
					s, _ := dm["parametersJsonSchema"].(map[string]any)
					if s == nil {
						s, _ = dm["parameters"].(map[string]any)
					}
					return "", s, true
				}
			}
			continue
		}
		if api == Chat {
			fn, _ := tm["function"].(map[string]any)
			if str(fn["name"]) == name {
				s, _ := fn["parameters"].(map[string]any)
				return "", s, true
			}
			continue
		}
		if str(tm["name"]) == name {
			s, _ := tm["input_schema"].(map[string]any)
			if s == nil {
				s, _ = tm["parameters"].(map[string]any)
			}
			return "", s, true
		}
		if tm["type"] == "namespace" {
			subs, _ := tm["tools"].([]any)
			for _, sub := range subs {
				sm, _ := sub.(map[string]any)
				if str(sm["name"]) == name {
					s, _ := sm["parameters"].(map[string]any)
					return str(tm["name"]), s, true
				}
			}
		}
	}
	return "", nil, false
}
