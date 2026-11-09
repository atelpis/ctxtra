package config

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/ctxtra/ctxtra/internal/experimental"
	"github.com/ctxtra/ctxtra/internal/source"
	"github.com/ctxtra/ctxtra/internal/testenv"
)

func mcpOnForTest(t *testing.T) { t.Setenv(experimental.MCPVar, "1") }

// problemsWith reports whether exactly one problem contains each of want, in
// order, and there are no others.
func problemsWith(t *testing.T, cfg *Config, want ...string) {
	t.Helper()
	if len(cfg.Problems) != len(want) {
		t.Fatalf("problems %q, want %d", cfg.Problems, len(want))
	}
	for i, w := range want {
		if !strings.Contains(cfg.Problems[i], w) {
			t.Errorf("problem %d: %s, want %q", i, cfg.Problems[i], w)
		}
	}
}

func serverNames(cfg *Config) []string {
	var out []string
	for _, s := range cfg.MCPServers {
		out = append(out, s.Name)
	}
	return out
}

func TestMCPServersGate(t *testing.T) {
	h := testenv.Home(t)
	path := testenv.Config(t, h, "schema: ctxtra/v1\nmcp-servers:\n  a: {url: https://x/mcp}\nbindings:\n  - {name: b, match: {global: true}, instructions: [x.md]}\n")
	cfg := load(path)
	if !cfg.Ignored() || len(cfg.MCPServers) != 0 || !strings.Contains(cfg.Problems[0], `unknown key "mcp-servers"`) {
		t.Errorf("without the variable, mcp-servers is an unknown key: %v", cfg.Problems)
	}
	mcpOnForTest(t)
	cfg = load(path)
	if len(cfg.Problems) > 0 || !slices.Equal(serverNames(cfg), []string{"a"}) {
		t.Errorf("with it: %v %v", cfg.Problems, cfg.MCPServers)
	}
}

func TestServerNames(t *testing.T) {
	for _, tc := range []struct {
		name string
		ok   bool
		hint string
	}{
		{"linear_work", true, ""},
		{"a", true, ""},
		{"gh2", true, ""},
		{strings.Repeat("a", 20), true, ""},
		{strings.Repeat("a", 21), false, "aaaaaaaaaaaaaaaaaaaa"},
		{"linear-work", false, "linear_work"},
		{"Linear", false, "linear"},
		{"2fa", false, "fa"},
		{"a__b", false, "a_b"},
		{"a_", false, "for example a"},
		{"_a", false, "for example a"},
		{"", false, ""},
	} {
		err := CheckServerName(tc.name)
		if (err == nil) != tc.ok {
			t.Errorf("%q: %v", tc.name, err)
		}
		if err != nil && !strings.Contains(err.Error(), "a-z, 0-9 and _") || tc.hint != "" && !strings.Contains(err.Error(), tc.hint) {
			t.Errorf("%q: %v, want hint %q", tc.name, err, tc.hint)
		}
	}
}

func TestMCPServers(t *testing.T) {
	mcpOnForTest(t)
	h := testenv.Home(t)
	cfg := load(testenv.Config(t, h, `schema: ctxtra/v1
mcp-servers:
  linear_work: {url: https://mcp.linear.app/mcp, notes: the work workspace}
  linear_ctxtra: {url: https://mcp.linear.app/mcp, scopes: [read, write]}
  local: {url: 'http://127.0.0.1:8080/mcp'}
  local6: {url: 'http://[::1]/mcp'}
  gh:
    url: https://api.githubcopilot.com/mcp/
    client-id: Iv23
    headers: {X-Team: core}
    secret-headers: [Authorization]
  pg:
    command: [npx, -y, server-postgres]
    env: {PGUSER: dev}
    env-pass: [HTTPS_PROXY]
    secret-env: [PGPASSWORD]
  abs: {command: [/usr/local/bin/mcp]}
  home: {command: ['~/bin/mcp', --stdio]}
  linear-work: {url: https://x/mcp}
  both: {url: https://x/mcp, command: [x]}
  neither: {notes: hi}
  plain: {url: http://example.com/mcp}
  rel: {command: [./bin/mcp]}
  rel2: {command: [bin/mcp]}
  mixed: {command: [x], headers: {A: b}}
  mixed2: {url: https://x/mcp, env: {A: b}}
  twice: {command: [x], env: {A: b}, secret-env: [A]}
  badvar: {command: [x], env-pass: [1A]}
  unknown: {url: https://x/mcp, nope: 1}
`))
	problemsWith(t, cfg,
		`line 19: mcp-servers.linear-work: server name "linear-work": names are 1 to 20 characters of a-z, 0-9 and _`,
		"line 20: mcp-servers.both: a server has url or command, not both",
		"line 21: mcp-servers.neither: a server needs url (a remote server) or command (a local one)",
		"line 22: mcp-servers.plain: url \"http://example.com/mcp\": remote servers need https",
		"line 23: mcp-servers.rel: command \"./bin/mcp\": the program must be an absolute path or a name in PATH",
		"line 24: mcp-servers.rel2: command \"bin/mcp\"",
		"line 25: mcp-servers.mixed: headers doesn't apply to a local server",
		"line 26: mcp-servers.mixed2: env doesn't apply to a remote server",
		"line 27: mcp-servers.twice: the variable A is set twice",
		`line 28: mcp-servers.badvar: env-pass: "1A" is not a valid name`,
		`line 29: mcp-servers.unknown: unknown key "nope"`,
	)
	if !strings.Contains(cfg.Problems[0], "for example linear_work; server skipped") {
		t.Errorf("suggestion: %s", cfg.Problems[0])
	}
	if got := serverNames(cfg); !slices.Equal(got, []string{"linear_work", "linear_ctxtra", "local", "local6", "gh", "pg", "abs", "home"}) {
		t.Fatalf("servers %v", got)
	}
	lw, _ := cfg.MCPServer("linear_work")
	lc, _ := cfg.MCPServer("linear_ctxtra")
	if lw.ID == lc.ID || lw.URL != lc.URL || lw.Notes != "the work workspace" || !slices.Equal(lc.Scopes, []string{"read", "write"}) {
		t.Errorf("two names for one service are separate servers: %+v %+v", lw, lc)
	}
	pg, _ := cfg.MCPServer("pg")
	if !pg.Local() || pg.Command[0] != "npx" || pg.Env["PGUSER"] != "dev" || !slices.Equal(pg.EnvPass, []string{"HTTPS_PROXY"}) ||
		!slices.Equal(pg.SecretEnv, []string{"PGPASSWORD"}) {
		t.Errorf("pg %+v", pg)
	}
	gh, _ := cfg.MCPServer("gh")
	if gh.ClientID != "Iv23" || gh.Headers["X-Team"] != "core" || !slices.Equal(gh.SecretHeaders, []string{"Authorization"}) {
		t.Errorf("gh %+v", gh)
	}
	if home, _ := cfg.MCPServer("home"); home.Command[0] != filepath.Join(h, "bin", "mcp") {
		t.Errorf("home %v", home.Command)
	}
}

func TestSecretLookingValues(t *testing.T) {
	mcpOnForTest(t)
	h := testenv.Home(t)
	cfg := load(testenv.Config(t, h, `schema: ctxtra/v1
mcp-servers:
  a: {url: https://x/mcp, headers: {Authorization: Bearer abc, X-Api-Key: k, X-Team: t}}
  b: {command: [x], env: {GITHUB_TOKEN: t, DB_PASSWORD: p, MY_SECRET_X: s, API_KEY: k, KEYBOARD: k, DB: dev}}
`))
	if len(cfg.Problems) > 0 || len(cfg.MCPServers) != 2 {
		t.Fatalf("secret-looking values are only warned about: %v", cfg.Problems)
	}
	want := []string{
		"a: headers.Authorization looks like a secret, which the config shouldn't hold; use secret-headers: [Authorization]",
		"a: headers.X-Api-Key", "b: env.API_KEY", "b: env.DB_PASSWORD", "b: env.GITHUB_TOKEN",
		"b: env.MY_SECRET_X looks like a secret, which the config shouldn't hold; use secret-env: [MY_SECRET_X]",
	}
	if len(cfg.Notes) != len(want) {
		t.Fatalf("notes %q", cfg.Notes)
	}
	for i, w := range want {
		if !strings.Contains(cfg.Notes[i], w) {
			t.Errorf("note %d: %s, want %q", i, cfg.Notes[i], w)
		}
	}
}

func TestBindingFileCantDeclareServers(t *testing.T) {
	mcpOnForTest(t)
	h := testenv.Home(t)
	cfg := setup(t, h, "schema: ctxtra/v1\nmcp-servers: {a: {command: [x]}}\n"+strings.TrimPrefix(head, "schema: ctxtra/v1\n")+
		"  - {name: b, match: {global: true}, instructions: [x.md]}\n")
	problemsWith(t, cfg, "line 2: mcp-servers works only in the config")
	if len(cfg.Bindings) != 0 || len(cfg.MCPServers) != 0 {
		t.Errorf("the whole file is skipped: %v %v", cfg.Bindings, cfg.MCPServers)
	}
}

func TestBindingServers(t *testing.T) {
	mcpOnForTest(t)
	h := testenv.Home(t)
	commit := strings.Repeat("a", 40)
	testenv.Write(t, filepath.Join(source.GenDir("https://h/team", commit), "b.yaml"),
		head+"  - {name: pinned, match: {global: true}, mcp-servers: [pg, github, broken, theirs], instructions: [x.md]}\n")
	testenv.Write(t, filepath.Join(h, ".config", "ctxtra", "b.yaml"),
		head+"  - {name: file, match: {global: true}, mcp-servers: [pg]}\n")
	cfg := load(testenv.Config(t, h, `schema: ctxtra/v1
sources:
  team: {git: https://h/team, branch: main, commit: '`+commit+`'}
mcp-servers:
  pg: {command: [pg-mcp]}
  github: {url: https://api.githubcopilot.com/mcp/}
  broken: {url: http://example.com}
bindings:
  - {name: only, match: {global: true}, mcp-servers: [github, pg]}
  - {name: typo, match: {global: true}, mcp-servers: [linear_wrok, pg, pg, Bad-Name, broken], instructions: [x.md]}
include: [b.yaml, ctxtra://team/b.yaml]
`))
	problemsWith(t, cfg,
		"line 7: mcp-servers.broken: url",
		"line 10: server linear_wrok is not in mcp-servers of ~/.config/ctxtra/config.yaml; skipped",
		"line 10: server pg is listed twice; skipped",
		`line 10: server name "Bad-Name"`,
		"line 10: server broken has an error in mcp-servers; skipped",
		"line 3: server pg: binding files of pinned sources can't turn on the config's servers; skipped",
		"line 3: server github: binding files of pinned sources can't turn on the config's servers",
		"line 3: server broken: binding files of pinned sources can't turn on the config's servers",
		"line 3: server theirs is not in mcp-servers",
	)
	if len(cfg.Bindings) != 4 {
		t.Fatalf("bindings %v", cfg.Bindings)
	}
	for i, want := range [][]string{{"github", "pg"}, {"pg"}, {"pg"}, nil} {
		if b := cfg.Bindings[i]; !slices.Equal(b.MCPServers, want) {
			t.Errorf("%s: %v, want %v", b.Name, b.MCPServers, want)
		}
	}
	if len(cfg.Bindings[1].Instructions) != 1 || len(cfg.Bindings[3].Instructions) != 1 {
		t.Errorf("the other entries still apply")
	}
}

func TestSourceCheckServerNames(t *testing.T) {
	mcpOnForTest(t)
	dir := t.TempDir()
	testenv.Write(t, filepath.Join(dir, "b.yaml"), head+"  - {name: a, match: {global: true}, mcp-servers: [linear, Bad]}\n")
	cfg := LoadSource(dir, "team", []string{filepath.Join(dir, "b.yaml")})
	if len(cfg.Problems) != 1 || !strings.Contains(cfg.Problems[0], `server name "Bad"`) {
		t.Errorf("only names are checked: %v", cfg.Problems)
	}
	if len(cfg.Bindings) != 1 || !slices.Equal(cfg.Bindings[0].MCPServers, []string{"linear"}) {
		t.Errorf("bindings %+v", cfg.Bindings)
	}
}
