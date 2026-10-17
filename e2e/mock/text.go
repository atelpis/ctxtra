package mock

import (
	"encoding/json"
	"regexp"
	"strings"
)

// BodyText flattens a request body to the text the model sees, each part
// prefixed with its role: the system prompt or instructions first, then the
// messages (or Responses input items, or Gemini contents) in order.
func BodyText(body map[string]any) string {
	var parts []string
	for _, key := range []string{"system", "instructions", "systemInstruction"} {
		v := body[key]
		if m, ok := v.(map[string]any); ok { // Gemini: {"parts": [...]}
			v = m["parts"]
		}
		switch v := v.(type) {
		case string:
			parts = append(parts, "["+key+"] "+v)
		case []any:
			for _, b := range v {
				if m, ok := b.(map[string]any); ok {
					parts = append(parts, "["+key+"] "+str(m["text"]))
				}
			}
		}
	}
	items, _ := body["messages"].([]any)
	if items == nil {
		items, _ = body["input"].([]any)
	}
	if items == nil {
		items, _ = body["contents"].([]any)
	}
	for _, it := range items {
		m, ok := it.(map[string]any)
		if !ok {
			continue
		}
		role := str(m["role"])
		if role == "" {
			role = str(m["type"])
		}
		c, ok := m["content"]
		if !ok {
			c = m["output"]
		}
		if c == nil {
			c = m["parts"] // Gemini
		}
		switch c := c.(type) {
		case string:
			parts = append(parts, "["+role+"] "+c)
		case []any:
			for _, b := range c {
				bm, ok := b.(map[string]any)
				if !ok {
					continue
				}
				t := str(bm["text"])
				if t == "" {
					t = jsonText(bm["content"])
				}
				if fr, ok := bm["functionResponse"].(map[string]any); ok { // Gemini
					t = jsonText(fr["response"])
				}
				parts = append(parts, "["+role+"] "+t)
			}
		}
	}
	return strings.Join(parts, "\n")
}

var markerRe = regexp.MustCompile(`CTXMARK-[A-Za-z0-9_]+`)

// Markers are the distinct CTXMARK-* tokens in text, in order of first
// appearance, without the CTXMARK- prefix.
func Markers(text string) []string {
	var out []string
	seen := map[string]bool{}
	for _, m := range markerRe.FindAllString(text, -1) {
		m = strings.TrimPrefix(m, "CTXMARK-")
		if !seen[m] {
			seen[m] = true
			out = append(out, m)
		}
	}
	return out
}

// Count is how many times CTXMARK-name appears in text.
func Count(text, name string) int {
	return len(regexp.MustCompile(`CTXMARK-`+regexp.QuoteMeta(name)+`\b`).FindAllStringIndex(text, -1))
}

// userTexts is the user-written text in a request, without tool results.
func userTexts(api API, body map[string]any) []string {
	var out []string
	key := map[API]string{Responses: "input", Gemini: "contents"}[api]
	if key == "" {
		key = "messages"
	}
	items, _ := body[key].([]any)
	for _, it := range items {
		m, ok := it.(map[string]any)
		if !ok || str(m["role"]) != "user" {
			continue
		}
		if api == Responses && m["type"] != nil && m["type"] != "message" {
			continue
		}
		content := m["content"]
		if api == Gemini {
			content = m["parts"]
		}
		switch c := content.(type) {
		case string:
			out = append(out, c)
		case []any:
			for _, b := range c {
				bm, ok := b.(map[string]any)
				if !ok {
					continue
				}
				if api != Responses && api != Gemini && str(bm["type"]) != "text" {
					continue
				}
				out = append(out, str(bm["text"]))
			}
		}
	}
	return out
}

// toolResults are the tool results in a request, oldest first, as text.
func toolResults(api API, body map[string]any) []string {
	var out []string
	switch api {
	case Anthropic:
		msgs, _ := body["messages"].([]any)
		for _, m := range msgs {
			mm, _ := m.(map[string]any)
			blocks, _ := mm["content"].([]any)
			for _, b := range blocks {
				if bm, ok := b.(map[string]any); ok && bm["type"] == "tool_result" {
					out = append(out, jsonText(bm["content"]))
				}
			}
		}
	case Chat:
		msgs, _ := body["messages"].([]any)
		for _, m := range msgs {
			if mm, ok := m.(map[string]any); ok && mm["role"] == "tool" {
				out = append(out, jsonText(mm["content"]))
			}
		}
	case Responses:
		items, _ := body["input"].([]any)
		for _, it := range items {
			if m, ok := it.(map[string]any); ok && (m["type"] == "function_call_output" || m["type"] == "custom_tool_call_output") {
				out = append(out, jsonText(m["output"]))
			}
		}
	case Gemini:
		contents, _ := body["contents"].([]any)
		for _, c := range contents {
			cm, _ := c.(map[string]any)
			parts, _ := cm["parts"].([]any)
			for _, p := range parts {
				if pm, ok := p.(map[string]any); ok && pm["functionResponse"] != nil {
					fr, _ := pm["functionResponse"].(map[string]any)
					out = append(out, jsonText(fr["response"]))
				}
			}
		}
	}
	return out
}

func str(v any) string {
	s, _ := v.(string)
	return s
}

func jsonText(v any) string {
	if v == nil {
		return ""
	}
	b, _ := json.Marshal(v)
	return string(b)
}
