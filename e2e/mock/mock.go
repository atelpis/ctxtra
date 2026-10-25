// Package mock is a model endpoint for agent tests. It speaks the Anthropic
// Messages, OpenAI Chat Completions, OpenAI Responses and Gemini
// generateContent APIs, streaming or not, records every request, and answers without a model: a text reply, or
// the next tool call of the test's script.
package mock

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

// Prompt is the main agent's task. It tells requests of the main agent
// apart from a subagent's.
const Prompt = "E2E-MAIN-TASK: reply with OK and nothing else."

// SubagentTask is the task of a subagent the script starts.
const SubagentTask = "SUBAGENT-PROBE: reply with OK and nothing else."

// Reply is the text answer to every request without a tool call.
const Reply = "MOCK-OK"

// API is the wire protocol of a request.
type API string

const (
	Anthropic API = "anthropic"
	Chat      API = "chat"
	Responses API = "responses"
	Gemini    API = "gemini"
)

// Role says whose request it is.
type Role string

const (
	Main     Role = "main"
	Subagent Role = "subagent"
	Side     Role = "side" // no tools offered: titles, summaries, classifiers
)

// Request is one recorded model request.
type Request struct {
	API   API
	Path  string
	Role  Role
	Text  string         // the model-visible text, see BodyText
	Body  map[string]any // the decoded request body
	Reply string         // "text", or the name of the tool the mock called
}

// Server is a running mock. It is safe for concurrent requests.
type Server struct {
	srv    *httptest.Server
	script []Step

	mu    sync.Mutex
	reqs  []Request
	calls int

	subagentSeen chan struct{} // closed at the first subagent request
	subagentOnce sync.Once
}

// subagentWait is how long the main agent's answer after a subagent's start
// waits for the subagent's first request. Antigravity runs subagents in the
// background and exits when the main agent is done, which can be before the
// subagent's first request.
const subagentWait = 30 * time.Second

// New starts a mock that runs script in the main agent, one step per model
// request, and stops it when the test ends.
func New(t testing.TB, script ...Step) *Server {
	s := &Server{script: script, subagentSeen: make(chan struct{})}
	s.srv = httptest.NewServer(http.HandlerFunc(s.handle))
	t.Cleanup(s.srv.Close)
	return s
}

// URL is the base URL, without a trailing slash and without /v1.
func (s *Server) URL() string { return s.srv.URL }

// Requests returns the model requests recorded so far, in arrival order.
func (s *Server) Requests() []Request {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Request(nil), s.reqs...)
}

func (s *Server) nextCallID() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	return s.calls
}

func apiOf(path string) (API, bool) {
	switch {
	case strings.HasSuffix(path, "/messages"):
		return Anthropic, true
	case strings.HasSuffix(path, "/chat/completions"):
		return Chat, true
	case strings.HasSuffix(path, "/responses"):
		return Responses, true
	case strings.HasSuffix(path, ":generateContent"), strings.HasSuffix(path, ":streamGenerateContent"):
		return Gemini, true
	}
	return "", false
}

func (s *Server) handle(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	switch {
	case r.Method == http.MethodHead:
		w.WriteHeader(http.StatusOK)
		return
	case r.Method == http.MethodGet && path == "/muse-code/models": // before the generic /models
		writeJSON(w, museCatalog)
		return
	case r.Method == http.MethodGet && strings.HasSuffix(strings.TrimSuffix(path, "/"), "/models"):
		writeJSON(w, map[string]any{"object": "list", "data": []any{
			map[string]any{"id": "mock-model", "object": "model", "type": "model", "display_name": "mock"}}})
		return
	case strings.HasSuffix(path, "/messages/count_tokens"):
		writeJSON(w, map[string]any{"input_tokens": 1})
		return
	}
	api, ok := apiOf(path)
	if r.Method != http.MethodPost || !ok {
		w.WriteHeader(http.StatusNotFound)
		writeJSON(w, map[string]any{"error": map[string]any{"type": "not_found", "message": "mock: " + path}})
		return
	}
	raw, _ := io.ReadAll(r.Body)
	body := map[string]any{}
	_ = json.Unmarshal(raw, &body)

	role := classify(api, body)
	var c *call
	switch role {
	case Main:
		if i := slices.Index(s.script, StartSubagent()); i >= 0 && len(toolResults(api, body)) > i {
			select {
			case <-s.subagentSeen:
			case <-time.After(subagentWait):
			case <-r.Context().Done():
			}
		}
		c = decide(api, body, s.script)
	case Subagent:
		s.subagentOnce.Do(func() { close(s.subagentSeen) })
	}
	reply := "text"
	if c != nil {
		reply = c.name
	}
	s.mu.Lock()
	s.reqs = append(s.reqs, Request{API: api, Path: path, Role: role, Text: BodyText(body), Body: body, Reply: reply})
	s.mu.Unlock()

	model, _ := body["model"].(string)
	stream, _ := body["stream"].(bool)
	if api == Gemini { // the model and streaming are in the path
		model, _, _ = strings.Cut(path[strings.LastIndex(path, "/")+1:], ":")
		stream = strings.HasSuffix(path, ":streamGenerateContent")
	}
	if model == "" {
		model = "mock-model"
	}
	text := replyText(body)
	var events []sse
	var whole any
	switch api {
	case Anthropic:
		whole, events = s.anthropic(model, c, text)
	case Chat:
		whole, events = s.chat(model, c, text)
	case Responses:
		whole, events = s.responses(model, c, text)
	case Gemini:
		whole, events = s.gemini(model, c, text)
		if stream && r.URL.Query().Get("alt") != "sse" {
			whole = []any{whole} // a JSON array of chunks
			stream = false
		}
	}
	if !stream {
		writeJSON(w, whole)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)
	f, _ := w.(http.Flusher)
	for _, e := range events {
		_, _ = w.Write(e.bytes())
		if f != nil {
			f.Flush()
		}
	}
}

// classify: a request without tools is a side call, and so is one that says
// they are unavailable (Copilot's compaction offers its tools anyway); a
// subagent's requests carry SubagentTask but not Prompt.
func classify(api API, body map[string]any) Role {
	if tools, _ := body["tools"].([]any); len(tools) == 0 {
		return Side
	}
	texts := strings.Join(userTexts(api, body), "\n")
	if strings.Contains(texts, "Tools are unavailable in this step") {
		return Side
	}
	if strings.Contains(texts, "SUBAGENT-PROBE") && !strings.Contains(texts, "E2E-MAIN-TASK") {
		return Subagent
	}
	return Main
}

// replyText is the text answer; agents' internal side calls get the format
// they parse.
func replyText(body map[string]any) string {
	b, _ := json.Marshal([]any{body["system"], body["messages"]})
	if strings.Contains(string(b), "<severity>N</severity>") {
		return "<severity>0</severity>" // Claude's auto-mode safety classifier: allow
	}
	return Reply
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

// museCatalog is the model catalog Muse Code reads from the origin of its
// base URL before the first request. It offers mock-model with tool calls
// and without reasoning, so Muse sends no reasoning parameters.
var museCatalog = map[string]any{"object": "list", "data": []any{map[string]any{
	"id": "mock-model", "object": "model", "created": 1, "owned_by": "mock",
	"metadata": map[string]any{"muse-code": map[string]any{
		"name": "mock-model", "family": "mock", "release_date": "2026-01-01", "is_hidden": false,
		"attachment": false, "reasoning": false, "temperature": false, "tool_call": true,
		"modalities": map[string]any{"input": []string{"text"}, "output": []string{"text"}},
		"limit":      map[string]any{"context": 200000, "output": 8192},
		"options":    map[string]any{}, "variants": map[string]any{}, "description": "mock",
		"cost": map[string]any{"input": "0", "output": "0", "cached": "0", "currency": "USD"},
	}},
}}}
