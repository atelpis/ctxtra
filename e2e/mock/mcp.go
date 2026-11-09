package mock

import "strings"

// CallMCPTools makes the agent call, in order, each offered tool whose name
// contains one of markers, with the name exactly as the request offers it.
// Markers whose tool isn't offered are skipped, so a tool the agent dropped
// or renamed past recognition doesn't end the script. It must be the last
// step.
func CallMCPTools(markers ...string) Step {
	return Step{kind: "mcp", name: strings.Join(markers, "\x00")}
}

// mcpSteps expands an "mcp" step into one "tool" step per offered tool.
func mcpSteps(api API, body map[string]any, s Step) []Step {
	var out []Step
	for _, m := range strings.Split(s.name, "\x00") {
		for _, t := range ToolNames(api, body) {
			if strings.Contains(t.Name, m) {
				out = append(out, Step{kind: "tool", name: t.Name, path: t.Namespace})
				break
			}
		}
	}
	return out
}

// Tool is a tool offered in a model request.
type Tool struct {
	Namespace, Name string
}

// ToolNames lists the tools a request offers, with Codex's namespaces.
func ToolNames(api API, body map[string]any) []Tool {
	var out []Tool
	tools, _ := body["tools"].([]any)
	for _, t := range tools {
		tm, _ := t.(map[string]any)
		switch {
		case api == Gemini:
			decls, _ := tm["functionDeclarations"].([]any)
			for _, d := range decls {
				dm, _ := d.(map[string]any)
				out = append(out, Tool{Name: str(dm["name"])})
			}
		case api == Chat:
			fn, _ := tm["function"].(map[string]any)
			out = append(out, Tool{Name: str(fn["name"])})
		case tm["type"] == "namespace":
			subs, _ := tm["tools"].([]any)
			for _, sub := range subs {
				sm, _ := sub.(map[string]any)
				out = append(out, Tool{Namespace: str(tm["name"]), Name: str(sm["name"])})
			}
		default:
			out = append(out, Tool{Name: str(tm["name"])})
		}
	}
	return out
}

// CallMCPToolsFlat is CallMCPTools for agents that take a namespaced tool
// back as one name, <namespace>__<tool>, without a namespace field.
func CallMCPToolsFlat(markers ...string) Step {
	return Step{kind: "mcp-flat", name: strings.Join(markers, "\x00")}
}

// CallMCPGeneric makes the agent call each of names on server through one
// generic tool (Antigravity's call_mcp_tool), with the names as the server
// lists them.
func CallMCPGeneric(tool, server string, names ...string) Step {
	return Step{kind: "mcp-generic", path: tool + "\x00" + server, name: strings.Join(names, "\x00")}
}

// CallMCPCode makes the agent call each of names on server inside a Code
// Mode tool (OpenCode's execute), as tools.<server>["<name>"](...).
func CallMCPCode(tool, server string, names ...string) Step {
	return Step{kind: "mcp-code", path: tool + "\x00" + server, name: strings.Join(names, "\x00")}
}

func expandMCP(api API, body map[string]any, s Step) []Step {
	switch s.kind {
	case "mcp":
		return mcpSteps(api, body, s)
	case "mcp-flat":
		var out []Step
		for _, st := range mcpSteps(api, body, s) {
			if st.path != "" {
				st.name = st.path + "__" + st.name
				st.path = ""
			}
			out = append(out, st)
		}
		return out
	case "mcp-generic", "mcp-code":
		tool, server, _ := strings.Cut(s.path, "\x00")
		var out []Step
		for _, n := range strings.Split(s.name, "\x00") {
			out = append(out, Step{kind: s.kind + "-call", name: tool, path: server + "\x00" + n})
		}
		return out
	}
	return nil
}

func genericCall(s Step) *call {
	server, name, _ := strings.Cut(s.path, "\x00")
	if s.kind == "mcp-code-call" {
		code := `return await tools.` + server + `[` + jsonQuote(name) + `]({note: "probe"})`
		return &call{name: s.name, args: map[string]any{"code": code}}
	}
	return &call{name: s.name, args: map[string]any{"ServerName": server, "ToolName": name,
		"Arguments": map[string]any{"note": "probe"}}}
}

func jsonQuote(s string) string { return `"` + strings.ReplaceAll(s, `"`, `\"`) + `"` }

// CallCode makes the agent run code with its Code Mode tool.
func CallCode(tool, code string) Step { return Step{kind: "code", name: tool, path: code} }
