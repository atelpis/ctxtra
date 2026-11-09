package config

import (
	"strings"
	"testing"
)

func TestMCPServerEdits(t *testing.T) {
	t.Setenv("CTXTRA_EXPERIMENTAL_MCP", "1")
	pg := NewMCPServer{Name: "pg", Command: []string{"npx", "-y", "@modelcontextprotocol/server-postgres"}, Env: [][2]string{{"PGUSER", "dev"}},
		EnvPass: []string{"HTTPS_PROXY"}, SecretEnv: []string{"PGPASSWORD"}, Notes: "the dev database"}
	lw := NewMCPServer{Name: "linear_work", URL: "https://mcp.linear.app/mcp", Scopes: []string{"read"}}
	start := "# my config\nschema: ctxtra/v1\n\nbindings:\n  - {name: a, match: {global: true}, instructions: [a.md]}\n"
	out, err := AddMCPServer([]byte(start), pg)
	if err != nil {
		t.Fatal(err)
	}
	want := start + "mcp-servers:\n  pg:\n    command: [npx, -y, '@modelcontextprotocol/server-postgres']\n    env: {PGUSER: dev}\n" +
		"    env-pass: [HTTPS_PROXY]\n    secret-env: [PGPASSWORD]\n    notes: the dev database\n"
	if string(out) != want {
		t.Errorf("got\n%s", out)
	}
	out, err = AddMCPServer(out, lw)
	if err != nil || !strings.HasSuffix(string(out), "  linear_work:\n    url: https://mcp.linear.app/mcp\n    scopes: [read]\n") {
		t.Errorf("%v\n%s", err, out)
	}
	if err := Check(out); err != nil {
		t.Errorf("invalid: %v", err)
	}
	if _, err := AddMCPServer(out, lw); err == nil {
		t.Errorf("added twice")
	}
	out, err = RemoveMCPServer(out, "pg")
	if err != nil || strings.Contains(string(out), "pg:") || !strings.Contains(string(out), "linear_work:") {
		t.Errorf("%v\n%s", err, out)
	}
	out, err = RemoveMCPServer(out, "linear_work")
	if err != nil || string(out) != start {
		t.Errorf("%v\n%q", err, out)
	}
	if _, err := RemoveMCPServer(out, "nope"); err == nil {
		t.Errorf("removed a server that isn't there")
	}
	b, err := AddBinding([]byte(start), NewBinding{Name: "w", Dirs: []string{"~/work/**"}, MCPServers: []string{"linear_work"}})
	if err != nil || !strings.HasSuffix(string(b), "  - name: w\n    match:\n      dirs:\n        - ~/work/**\n    mcp-servers:\n      - linear_work\n") {
		t.Errorf("%v\n%s", err, b)
	}
}
