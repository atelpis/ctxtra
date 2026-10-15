package mock

import (
	"bufio"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// The mock decides like the lab's mockllm.py did on requests recorded from
// real agents: testdata/poc holds such requests, reduced to the parts the
// decision reads (paths and long texts replaced), with the role and reply the
// Python mock gave.
func TestParityWithLabMock(t *testing.T) {
	files, _ := filepath.Glob("testdata/poc/*.json")
	if len(files) == 0 {
		t.Fatal("no fixtures")
	}
	for _, f := range files {
		t.Run(filepath.Base(f), func(t *testing.T) {
			var fx struct {
				API    API
				Script []string
				Role   Role
				Reply  json.RawMessage
				Body   map[string]any
			}
			b, err := os.ReadFile(f)
			if err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(b, &fx); err != nil {
				t.Fatal(err)
			}
			var script []Step
			for _, s := range fx.Script {
				if p, ok := strings.CutPrefix(s, "read:"); ok {
					script = append(script, Read(p))
				} else {
					script = append(script, StartSubagent())
				}
			}
			if role := classify(fx.API, fx.Body); role != fx.Role {
				t.Fatalf("role %s, want %s", role, fx.Role)
			}
			var c *call
			if fx.Role == Main {
				c = decide(fx.API, fx.Body, script)
			}
			var want struct {
				Tool string
				Args map[string]any
			}
			if string(fx.Reply) != `"text"` {
				if err := json.Unmarshal(fx.Reply, &want); err != nil {
					t.Fatal(err)
				}
			}
			switch {
			case c == nil && want.Tool != "":
				t.Fatalf("text reply, want tool %s", want.Tool)
			case c != nil && c.name != want.Tool:
				t.Fatalf("tool %s, want %q", c.name, want.Tool)
			case c != nil:
				got, _ := json.Marshal(c.args)
				var norm map[string]any
				_ = json.Unmarshal(got, &norm)
				if _, ok := want.Args["description"]; ok {
					want.Args["description"] = norm["description"] // the lab named the probe differently
				}
				if !reflect.DeepEqual(norm, want.Args) {
					t.Fatalf("args %v, want %v", norm, want.Args)
				}
			}
		})
	}
}

// Every API answers a streaming request with events a client can parse,
// and the request is recorded with its text and role.
func TestStreamingRoundTrip(t *testing.T) {
	tools := []any{map[string]any{"name": "Read", "input_schema": map[string]any{}},
		map[string]any{"type": "function", "name": "exec_command", "parameters": map[string]any{}},
		map[string]any{"type": "function", "function": map[string]any{"name": "read"}}}
	for _, tc := range []struct {
		path, last string
		body       map[string]any
	}{
		{"/v1/messages", "message_stop", map[string]any{"stream": true, "tools": tools,
			"messages": []any{map[string]any{"role": "user", "content": Prompt + " CTXMARK-A"}}}},
		{"/v1/chat/completions", "[DONE]", map[string]any{"stream": true, "tools": tools,
			"messages": []any{map[string]any{"role": "user", "content": Prompt + " CTXMARK-A"}}}},
		{"/v1/responses", "response.completed", map[string]any{"stream": true, "tools": tools,
			"input": []any{map[string]any{"type": "message", "role": "user",
				"content": []any{map[string]any{"type": "input_text", "text": Prompt + " CTXMARK-A"}}}}}},
	} {
		t.Run(tc.path, func(t *testing.T) {
			s := New(t, Read("/x"))
			b, _ := json.Marshal(tc.body)
			resp, err := http.Post(s.URL()+tc.path, "application/json", strings.NewReader(string(b)))
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			var last string
			sc := bufio.NewScanner(resp.Body)
			for sc.Scan() {
				data, ok := strings.CutPrefix(sc.Text(), "data: ")
				if !ok {
					continue
				}
				if data == "[DONE]" {
					last = data
					continue
				}
				var ev map[string]any
				if err := json.Unmarshal([]byte(data), &ev); err != nil {
					t.Fatalf("bad event %q: %v", data, err)
				}
				last, _ = ev["type"].(string)
			}
			if last != tc.last {
				t.Errorf("last event %q, want %q", last, tc.last)
			}
			reqs := s.Requests()
			if len(reqs) != 1 || reqs[0].Role != Main || reqs[0].Reply == "text" ||
				!reflect.DeepEqual(Markers(reqs[0].Text), []string{"A"}) {
				t.Errorf("recorded %+v", reqs)
			}
		})
	}
}
