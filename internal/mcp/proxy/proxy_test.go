package proxy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/ctxtra/ctxtra/internal/mcp/mcptest"
	"github.com/ctxtra/ctxtra/internal/mcp/names"
	"github.com/ctxtra/ctxtra/internal/mcp/upstream"
)

// The test binary is also the test MCP server (mcptest), and a proxy
// process for the tests that kill it.
func TestMain(m *testing.M) {
	switch os.Getenv("PROXYTEST_ROLE") {
	case "server":
		mcptest.Main(os.Args[1:])
		os.Exit(0)
	case "proxy":
		var servers []upstream.Server
		_ = json.Unmarshal([]byte(os.Getenv("PROXYTEST_SERVERS")), &servers)
		dir, _ := os.Getwd()
		_ = Run(context.Background(), Options{Agent: "test", Dir: dir,
			Servers: func(string) ([]upstream.Server, []string) { return servers, nil }},
			&mcp.StdioTransport{})
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// testServer is a local server that runs mcptest with args.
func testServer(t *testing.T, name string, args ...string) upstream.Server {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return upstream.Server{ID: name, Name: name, Command: append([]string{exe}, args...),
		Env: map[string]string{"PROXYTEST_ROLE": "server"}}
}

// session is an agent connected to a proxy.
type session struct {
	cs      *mcp.ClientSession
	changed chan struct{}
	logMu   sync.Mutex
	log     []Record
	done    chan error
	prog    chan *mcp.ProgressNotificationParams
}

func (s *session) records() []Record {
	s.logMu.Lock()
	defer s.logMu.Unlock()
	return slices.Clone(s.log)
}

// start runs a proxy for dir with servers, and connects to it.
func start(t *testing.T, dir string, deadline time.Duration, servers ...upstream.Server) *session {
	t.Helper()
	s := &session{changed: make(chan struct{}, 10), done: make(chan error, 1), prog: make(chan *mcp.ProgressNotificationParams, 10)}
	ct, st := mcp.NewInMemoryTransports()
	o := Options{Agent: "test", Dir: dir, Deadline: deadline,
		Servers: func(string) ([]upstream.Server, []string) { return servers, nil },
		Log: func(r Record) {
			s.logMu.Lock()
			s.log = append(s.log, r)
			s.logMu.Unlock()
		}}
	go func() { s.done <- Run(context.Background(), o, st) }()
	c := mcp.NewClient(&mcp.Implementation{Name: "agent", Version: "1"}, &mcp.ClientOptions{
		ToolListChangedHandler: func(context.Context, *mcp.ToolListChangedRequest) {
			s.changed <- struct{}{}
		},
		ProgressNotificationHandler: func(_ context.Context, r *mcp.ProgressNotificationClientRequest) {
			s.prog <- r.Params
		},
	})
	cs, err := c.Connect(context.Background(), ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	s.cs = cs
	t.Cleanup(func() { s.close(t) })
	return s
}

func (s *session) close(t *testing.T) {
	_ = s.cs.Close()
	select {
	case err := <-s.done:
		if err != nil {
			t.Errorf("proxy: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Errorf("the proxy didn't stop")
	}
	s.done <- nil // for a second close
}

func (s *session) tools(t *testing.T) map[string]*mcp.Tool {
	t.Helper()
	res, err := s.cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]*mcp.Tool{}
	for _, tool := range res.Tools {
		out[tool.Name] = tool
	}
	return out
}

func toolNames(m map[string]*mcp.Tool) []string {
	var out []string
	for n := range m {
		out = append(out, n)
	}
	slices.Sort(out)
	return out
}

func (s *session) call(t *testing.T, name string, args any) (string, error) {
	t.Helper()
	res, err := s.cs.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		return "", err
	}
	var b strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			b.WriteString(tc.Text)
		}
	}
	if res.IsError {
		return b.String(), fmt.Errorf("tool error: %s", b.String())
	}
	return b.String(), nil
}

func TestNoServers(t *testing.T) {
	s := start(t, t.TempDir(), time.Second)
	if got := s.tools(t); len(got) != 0 {
		t.Errorf("tools %v", got)
	}
	caps := s.cs.InitializeResult().Capabilities
	if caps.Tools == nil || caps.Prompts != nil || caps.Resources != nil {
		t.Errorf("only tools: %+v", caps)
	}
}

// Local servers start in the session folder with only their own and the
// base variables.
func TestLocalEnvironment(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "do-not-pass")
	t.Setenv("HTTPS_PROXY", "http://proxy:3128")
	t.Setenv("MY_PASS", "passed")
	dir := t.TempDir()
	srv := testServer(t, "pg")
	srv.Env["DB"] = "dev"
	srv.EnvPass = []string{"MY_PASS"}
	s := start(t, dir, 5*time.Second, srv)
	out, err := s.call(t, "pg__env", nil)
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Cwd string
		Env []string
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatal(out)
	}
	real, _ := filepath.EvalSymlinks(dir)
	if wd, _ := filepath.EvalSymlinks(got.Cwd); wd != real {
		t.Errorf("cwd %s, want %s", got.Cwd, dir)
	}
	var keys []string
	for _, kv := range got.Env {
		k, _, _ := strings.Cut(kv, "=")
		keys = append(keys, k)
	}
	for _, want := range []string{"DB=dev", "HTTPS_PROXY=http://proxy:3128", "MY_PASS=passed", "PATH=" + os.Getenv("PATH")} {
		if !slices.Contains(got.Env, want) {
			t.Errorf("missing %s in %v", want, got.Env)
		}
	}
	for _, k := range keys {
		if !slices.Contains(baseEnv, k) && !slices.Contains([]string{"DB", "MY_PASS", "PROXYTEST_ROLE"}, k) {
			t.Errorf("unexpected variable %s", k)
		}
	}
}

// The first tool list waits for every server until the deadline; a late
// server's tools follow with a list change.
func TestSlowServer(t *testing.T) {
	s := start(t, t.TempDir(), 1500*time.Millisecond, testServer(t, "fast"), testServer(t, "slow", "-delay", "3s"))
	began := time.Now()
	first := toolNames(s.tools(t))
	if took := time.Since(began); took > 2500*time.Millisecond {
		t.Errorf("the first list took %v", took)
	}
	if !slices.Contains(first, "fast__echo") || slices.ContainsFunc(first, func(n string) bool { return strings.HasPrefix(n, "slow__") }) {
		t.Fatalf("first list %v", first)
	}
	deadline := time.After(10 * time.Second)
	for {
		select {
		case <-s.changed:
		case <-deadline:
			t.Fatalf("no list change with the slow server's tools")
		}
		if slices.Contains(toolNames(s.tools(t)), "slow__echo") {
			break
		}
	}
	var events []string
	for _, r := range s.records() {
		events = append(events, r.Event+":"+r.Server+strings.Join(r.Servers, ","))
	}
	if !slices.Contains(events, "deadline:slow") || !slices.Contains(events, "late:slow") {
		t.Errorf("log %v", events)
	}
}

// Without slow servers, the first list doesn't wait for the deadline.
func TestAllInTime(t *testing.T) {
	s := start(t, t.TempDir(), 20*time.Second, testServer(t, "a"), testServer(t, "b"))
	began := time.Now()
	got := toolNames(s.tools(t))
	if time.Since(began) > 10*time.Second || !slices.Contains(got, "a__echo") || !slices.Contains(got, "b__echo") {
		t.Errorf("%v after %v", got, time.Since(began))
	}
}

// A shortened name reaches the server with its own name and the same
// arguments; the tool keeps its title and annotations.
func TestCalls(t *testing.T) {
	s := start(t, t.TempDir(), 5*time.Second, testServer(t, "gh", "-tools", "create-issue"))
	tools := s.tools(t)
	n, _ := names.Assign([]names.Tool{{Server: "gh", Name: "create-issue"}})
	shown := n[0].Shown
	if tools[shown] == nil || !strings.HasPrefix(tools[shown].Description, "[gh: create-issue] ") {
		t.Fatalf("tools %v", toolNames(tools))
	}
	if e := tools["gh__echo"]; e == nil || e.Title != "Echo" || e.Annotations == nil || !e.Annotations.ReadOnlyHint || e.Description != "Echo the arguments." {
		t.Errorf("echo %+v", e)
	}
	if out, err := s.call(t, shown, nil); err != nil || out != "ok create-issue" {
		t.Errorf("%q %v", out, err)
	}
	if out, err := s.call(t, "gh__echo", map[string]any{"title": "x"}); err != nil || out != `echo {"title":"x"}` {
		t.Errorf("%q %v", out, err)
	}
	if _, err := s.call(t, "gh__nope", nil); err == nil {
		t.Errorf("an unknown tool")
	}
}

// Cancelling a call reaches the server; its progress reaches the agent.
func TestCancelAndProgress(t *testing.T) {
	s := start(t, t.TempDir(), 5*time.Second, testServer(t, "s"))
	s.tools(t)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		p := &mcp.CallToolParams{Name: "s__slow"}
		p.SetProgressToken("tok-1")
		_, err := s.cs.CallTool(ctx, p)
		done <- err
	}()
	select {
	case p := <-s.prog:
		if p.ProgressToken != "tok-1" || p.Message != "started" {
			t.Errorf("progress %+v", p)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("no progress")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("the cancelled call didn't end")
	}
	// The server is still usable: its handler was cancelled, not stuck.
	if out, err := s.call(t, "s__echo", nil); err != nil || !strings.HasPrefix(out, "echo") {
		t.Errorf("%q %v", out, err)
	}
}

func alive(pid int) bool { return syscall.Kill(pid, 0) == nil }

func readPid(t *testing.T, path string) int {
	t.Helper()
	for range 100 {
		if b, err := os.ReadFile(path); err == nil && len(b) > 0 {
			pid, _ := strconv.Atoi(string(b))
			return pid
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("no pid in %s", path)
	return 0
}

func waitGone(t *testing.T, pid int, within time.Duration) {
	t.Helper()
	for end := time.Now().Add(within); time.Now().Before(end); time.Sleep(50 * time.Millisecond) {
		if !alive(pid) {
			return
		}
	}
	t.Errorf("process %d still runs", pid)
}

// When the agent closes the proxy's input, every server stops: one that
// ignores SIGTERM gets SIGKILL.
func TestStopOnClose(t *testing.T) {
	dir := t.TempDir()
	a, b := filepath.Join(dir, "a.pid"), filepath.Join(dir, "b.pid")
	s := start(t, dir, 5*time.Second, testServer(t, "a", "-pidfile", a), testServer(t, "b", "-pidfile", b, "-ignore-term"))
	s.tools(t)
	pa, pb := readPid(t, a), readPid(t, b)
	began := time.Now()
	s.close(t)
	if took := time.Since(began); took > 6*time.Second {
		t.Errorf("stop took %v", took)
	}
	// A process that has exited stays a zombie until reaped; its parent,
	// the test binary, reaps it with Wait.
	waitGone(t, pa, 3*time.Second)
	waitGone(t, pb, 3*time.Second)
}

// A proxy killed with SIGKILL can't stop its servers: they get the end of
// their input, and a server that exits then stops, as with native MCP.
func TestProxyKilled(t *testing.T) {
	dir := t.TempDir()
	pidfile := filepath.Join(dir, "a.pid")
	servers, _ := json.Marshal([]upstream.Server{testServer(t, "a", "-pidfile", pidfile)})
	exe, _ := os.Executable()
	cmd := exec.Command(exe)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "PROXYTEST_ROLE=proxy", "PROXYTEST_SERVERS="+string(servers))
	c := mcp.NewClient(&mcp.Implementation{Name: "agent", Version: "1"}, nil)
	cs, err := c.Connect(context.Background(), &mcp.CommandTransport{Command: cmd}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := cs.ListTools(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	pid := readPid(t, pidfile)
	_ = cmd.Process.Kill()
	_ = cs.Close()
	// The server isn't our child: once it exits, init reaps it.
	waitGone(t, pid, 5*time.Second)
}

// One server that fails, and one tool the SDK can't serve, leave the rest.
func TestFailingServer(t *testing.T) {
	bad := upstream.Server{ID: "bad", Name: "bad", Command: []string{"/nonexistent/mcp"}}
	exits := testServer(t, "exits")
	exits.Command = []string{"/bin/sh", "-c", "exit 3"}
	secret := testServer(t, "secret")
	secret.SecretEnv = []string{"TOKEN"}
	remote := upstream.Server{ID: "r", Name: "r", URL: "https://example.com/mcp"}
	s := start(t, t.TempDir(), 5*time.Second, bad, testServer(t, "good"), exits, secret, remote)
	got := toolNames(s.tools(t))
	if len(got) != 6 || !slices.Contains(got, "good__echo") {
		t.Errorf("tools %v", got)
	}
	errs := map[string]string{}
	for _, r := range s.records() {
		if r.Event == "connect" {
			errs[r.Server] = r.Error
		}
	}
	want := map[string]string{"bad": "program not found", "good": "", "exits": "connection closed",
		"secret": "secret values are not available", "r": "remote servers are not supported yet"}
	for k, v := range want {
		if errs[k] != v && (k != "exits" || !strings.HasPrefix(errs[k], "server exited")) {
			t.Errorf("%s: %q, want %q", k, errs[k], v)
		}
	}
}

func TestBadToolSchema(t *testing.T) {
	ct, st := mcp.NewInMemoryTransports()
	srv := mcp.NewServer(&mcp.Implementation{Name: "x", Version: "1"}, nil)
	srv.AddTool(&mcp.Tool{Name: "ok", InputSchema: map[string]any{"type": "object"}}, nil)
	go func() { _ = srv.Run(context.Background(), st) }()
	connect := func(ctx context.Context, s upstream.Server, _ string, c *mcp.Client) (*Upstream, error) {
		cs, err := c.Connect(ctx, ct, nil)
		if err != nil {
			return nil, err
		}
		return &Upstream{Server: s, Session: cs}, nil
	}
	p := &proxy{o: Options{Log: func(Record) {}}, tools: map[string]route{}, latest: map[string][]*mcp.Tool{}, ready: make(chan struct{})}
	p.server = mcp.NewServer(&mcp.Implementation{Name: "ctxtra", Version: "1"}, nil)
	up, err := connect(context.Background(), upstream.Server{Name: "x"}, "", mcp.NewClient(&mcp.Implementation{Name: "c"}, nil))
	if err != nil {
		t.Fatal(err)
	}
	defer up.Close()
	p.add(up)
	p.setTools(up, []*mcp.Tool{{Name: "ok", InputSchema: map[string]any{"type": "object"}}, {Name: "bad", InputSchema: map[string]any{"type": "string"}}})
	if _, ok := p.tools["x__ok"]; !ok || len(p.tools) != 1 {
		t.Errorf("tools %v", p.tools)
	}
	// A tool that changes to a schema the SDK can't serve is gone from the
	// list too, not listed with its old schema.
	p.setTools(up, []*mcp.Tool{{Name: "ok", InputSchema: map[string]any{"type": "string"}}})
	ct2, st2 := mcp.NewInMemoryTransports()
	go func() { _ = p.server.Run(context.Background(), st2) }()
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "agent"}, nil).Connect(context.Background(), ct2, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()
	p.open()
	if res, err := cs.ListTools(context.Background(), nil); err != nil || len(res.Tools) != 0 || len(p.tools) != 0 {
		t.Errorf("%v %+v %v", err, res, p.tools)
	}
}

// The log has no arguments, results or error messages of calls.
func TestLogHasNoContent(t *testing.T) {
	const marker = "SECRET-MARKER-7f3a"
	s := start(t, t.TempDir(), 5*time.Second, testServer(t, "s"))
	s.tools(t)
	if out, err := s.call(t, "s__echo", map[string]any{"title": marker}); err != nil || !strings.Contains(out, marker) {
		t.Fatalf("%q %v", out, err)
	}
	if _, err := s.call(t, "s__fail", map[string]any{"title": marker}); err == nil {
		t.Fatal("fail didn't fail")
	}
	s.close(t)
	b, _ := json.Marshal(s.records())
	if strings.Contains(string(b), marker) {
		t.Errorf("the log has the marker: %s", b)
	}
	if !strings.Contains(string(b), `"event":"call","server":"s","error":"error code 0"`) {
		t.Errorf("the failed call isn't logged by kind: %s", b)
	}
	if !strings.Contains(string(b), `"event":"start"`) || !strings.Contains(string(b), `"event":"exit"`) {
		t.Errorf("start and exit: %s", b)
	}
}

// A call that hangs in its server doesn't keep the proxy or the server
// running when the agent closes the proxy's input.
func TestStopWithHangingCall(t *testing.T) { stopWithCall(t, false) }

// Neither does an answer that the agent stopped reading.
func TestStopWithUnreadAnswer(t *testing.T) { stopWithCall(t, true) }

func stopWithCall(t *testing.T, big bool) {
	dir := t.TempDir()
	pidfile := filepath.Join(dir, "a.pid")
	servers, _ := json.Marshal([]upstream.Server{testServer(t, "a", "-pidfile", pidfile, "-ignore-term")})
	exe, _ := os.Executable()
	cmd := exec.Command(exe)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "PROXYTEST_ROLE=proxy", "PROXYTEST_SERVERS="+string(servers))
	in, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	tool := "a__hang"
	if big {
		tool = "a__big"
	}
	for _, line := range []string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"t","version":"1"}}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"` + tool + `","arguments":{}}}`,
	} {
		if _, err := in.Write([]byte(line + "\n")); err != nil {
			t.Fatal(err)
		}
	}
	// Wait for the tool list, so the call reached the server.
	dec := json.NewDecoder(out)
	for {
		var m map[string]any
		if err := dec.Decode(&m); err != nil {
			t.Fatal(err)
		}
		if m["id"] == float64(2) {
			break
		}
	}
	pid := readPid(t, pidfile)
	time.Sleep(300 * time.Millisecond)
	began := time.Now()
	_ = in.Close()
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case <-done:
	case <-time.After(8 * time.Second):
		_ = cmd.Process.Kill()
		t.Fatal("the proxy didn't exit")
	}
	if took := time.Since(began); took > 6*time.Second {
		t.Errorf("stop took %v", took)
	}
	waitGone(t, pid, 3*time.Second)
}

// A server that hangs before it answers initialize is stopped too.
func TestStopWhileConnecting(t *testing.T) {
	dir := t.TempDir()
	pidfile := filepath.Join(dir, "a.pid")
	s := start(t, dir, 500*time.Millisecond, testServer(t, "a", "-pidfile", pidfile, "-ignore-term", "-delay", "1h"))
	s.tools(t)
	pid := readPid(t, pidfile)
	began := time.Now()
	s.close(t)
	waitGone(t, pid, 3*time.Second)
	if took := time.Since(began); took > 3*time.Second {
		t.Errorf("stopped after %v", took)
	}
}

// A process that a server started, and that ignores SIGTERM, doesn't
// outlive the session.
func TestStopDescendants(t *testing.T) {
	dir := t.TempDir()
	child := filepath.Join(dir, "child.pid")
	s := start(t, dir, 5*time.Second, testServer(t, "a", "-child", child))
	s.tools(t)
	pid := readPid(t, child)
	s.close(t)
	waitGone(t, pid, 4*time.Second)
}

// The deadline covers resolving the servers: a slow resolution answers
// the first list without tools, and the tools follow.
func TestSlowResolution(t *testing.T) {
	ct, st := mcp.NewInMemoryTransports()
	srv := testServer(t, "a")
	o := Options{Agent: "test", Dir: t.TempDir(), Deadline: 300 * time.Millisecond,
		Servers: func(string) ([]upstream.Server, []string) {
			time.Sleep(1500 * time.Millisecond)
			return []upstream.Server{srv}, nil
		}}
	done := make(chan error, 1)
	go func() { done <- Run(context.Background(), o, st) }()
	changed := make(chan struct{}, 10)
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "agent"}, &mcp.ClientOptions{
		ToolListChangedHandler: func(context.Context, *mcp.ToolListChangedRequest) { changed <- struct{}{} },
	}).Connect(context.Background(), ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	began := time.Now()
	res, err := cs.ListTools(context.Background(), nil)
	if err != nil || len(res.Tools) != 0 || time.Since(began) > time.Second {
		t.Fatalf("%v %v after %v", res, err, time.Since(began))
	}
	select {
	case <-changed:
	case <-time.After(10 * time.Second):
		t.Fatal("no list change")
	}
	_ = cs.Close()
	<-done
}

// A server whose tools change while another server is still starting:
// the first list has its new tools.
func TestToolChangeBeforeFirstList(t *testing.T) {
	fast := mcp.NewServer(&mcp.Implementation{Name: "fast"}, nil)
	obj := map[string]any{"type": "object"}
	fast.AddTool(&mcp.Tool{Name: "old", InputSchema: obj}, func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return &mcp.CallToolResult{}, nil
	})
	connect := func(ctx context.Context, s upstream.Server, _ string, c *mcp.Client) (*Upstream, error) {
		if s.Name == "slow" {
			select {
			case <-time.After(time.Second):
			case <-ctx.Done():
			}
			return nil, errors.New("slow")
		}
		ct, st := mcp.NewInMemoryTransports()
		go func() { _ = fast.Run(context.Background(), st) }()
		cs, err := c.Connect(ctx, ct, nil)
		if err != nil {
			return nil, err
		}
		go func() {
			time.Sleep(200 * time.Millisecond)
			fast.RemoveTools("old")
			fast.AddTool(&mcp.Tool{Name: "new", InputSchema: obj}, func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				return &mcp.CallToolResult{}, nil
			})
		}()
		return &Upstream{Server: s, Session: cs}, nil
	}
	ct, st := mcp.NewInMemoryTransports()
	o := Options{Agent: "test", Dir: t.TempDir(), Deadline: 5 * time.Second, Connect: connect,
		Servers: func(string) ([]upstream.Server, []string) {
			return []upstream.Server{{ID: "fast", Name: "fast", Command: []string{"x"}}, {ID: "slow", Name: "slow", Command: []string{"x"}}}, nil
		}}
	done := make(chan error, 1)
	go func() { done <- Run(context.Background(), o, st) }()
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "agent"}, nil).Connect(context.Background(), ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	res, err := cs.ListTools(context.Background(), nil)
	if err != nil || len(res.Tools) != 1 || res.Tools[0].Name != "fast__new" {
		t.Errorf("%v %+v", err, res)
	}
	_ = cs.Close()
	<-done
}

// A server that answers its first tool list after the deadline, and
// changes its tools before that: the session gets the new tools.
func TestLateServerChanged(t *testing.T) {
	up := mcp.NewServer(&mcp.Implementation{Name: "s"}, nil)
	obj := map[string]any{"type": "object"}
	noop := func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return &mcp.CallToolResult{}, nil
	}
	up.AddTool(&mcp.Tool{Name: "old", InputSchema: obj}, noop)
	var lists atomic.Int32
	up.AddReceivingMiddleware(func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			if method == "tools/list" && lists.Add(1) == 1 {
				res, err := next(ctx, method, req) // the list as it is now, answered late
				time.Sleep(1500 * time.Millisecond)
				return res, err
			}
			return next(ctx, method, req)
		}
	})
	connect := func(ctx context.Context, s upstream.Server, _ string, c *mcp.Client) (*Upstream, error) {
		ct, st := mcp.NewInMemoryTransports()
		go func() { _ = up.Run(context.Background(), st) }()
		cs, err := c.Connect(ctx, ct, nil)
		if err != nil {
			return nil, err
		}
		go func() {
			time.Sleep(300 * time.Millisecond)
			up.RemoveTools("old")
			up.AddTool(&mcp.Tool{Name: "new", InputSchema: obj}, noop)
		}()
		return &Upstream{Server: s, Session: cs}, nil
	}
	ct, st := mcp.NewInMemoryTransports()
	o := Options{Agent: "test", Dir: t.TempDir(), Deadline: 200 * time.Millisecond, Connect: connect,
		Servers: func(string) ([]upstream.Server, []string) {
			return []upstream.Server{{ID: "s", Name: "s", Command: []string{"x"}}}, nil
		}}
	done := make(chan error, 1)
	go func() { done <- Run(context.Background(), o, st) }()
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "agent"}, nil).Connect(context.Background(), ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for end := time.Now().Add(5 * time.Second); time.Now().Before(end); time.Sleep(100 * time.Millisecond) {
		res, err := cs.ListTools(context.Background(), nil)
		if err != nil {
			t.Fatal(err)
		}
		names = nil
		for _, tool := range res.Tools {
			names = append(names, tool.Name)
		}
		if len(names) > 0 && lists.Load() >= 2 && time.Since(end.Add(-5*time.Second)) > 2500*time.Millisecond {
			break
		}
	}
	if !slices.Equal(names, []string{"s__new"}) {
		t.Errorf("tools %v", names)
	}
	_ = cs.Close()
	<-done
}
