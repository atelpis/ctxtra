package e2e

// Agent contract tests: each scenario runs real agents (Claude Code, Codex,
// Pi, OpenCode, Copilot, Muse Code, Antigravity, Hermes, DeepSeek Harness)
// non-interactively against the mock model, with ctxtra set up in an
// isolated home folder, and checks what reaches the model.
//
//	go test -count=1 ./...                 every agent found
//	go test -count=1 . -agent claude       one agent
//	CTXTRA_E2E_REQUIRE=1                 a missing agent fails instead of skipping (CI)
//	CTXTRA_E2E_EVIDENCE=DIR              save everything about a failed run in DIR
//	go test -count=1 ./... -keep           keep fixture folders and print where
//
// Always use -count=1: the test cache doesn't know about ctxtra's sources
// or the agent binaries, so a cached pass can hide a change in either.

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/atelpis/ctxtra/e2e/driver"
	"github.com/atelpis/ctxtra/e2e/fixture"
	"github.com/atelpis/ctxtra/e2e/mock"
)

var (
	agentFlag  = flag.String("agent", "", "run only this agent (claude, codex, pi, opencode, copilot, muse, antigravity, hermes, dsh)")
	keepFlag   = flag.Bool("keep", false, "keep each run's home folder and log its path")
	timeoutRun = flag.Duration("run-timeout", 3*time.Minute, "timeout of one agent run")
)

// ctxtraBin is the ctxtra binary under test, built by TestMain.
var ctxtraBin string

func TestMain(m *testing.M) {
	flag.Parse()
	code, err := run(m)
	if err != nil {
		fmt.Fprintln(os.Stderr, "e2e:", err)
		code = 1
	}
	os.Exit(code)
}

func run(m *testing.M) (int, error) {
	if *agentFlag != "" && !slices.ContainsFunc(driver.All, func(d driver.Driver) bool { return d.Name() == *agentFlag }) {
		return 0, fmt.Errorf("-agent %q: no such driver", *agentFlag)
	}
	// Not under TMPDIR as the fixtures see it: setup refuses a binary in a
	// temporary folder.
	dir, err := os.MkdirTemp("", "ctxtra-e2e-bin-")
	if err != nil {
		return 0, err
	}
	defer os.RemoveAll(dir)
	ctxtraBin = filepath.Join(dir, "ctxtra")
	build := exec.Command("go", "build", "-o", ctxtraBin, "./cmd/ctxtra")
	build.Dir = ".."
	if out, err := build.CombinedOutput(); err != nil {
		return 0, fmt.Errorf("building ctxtra: %v\n%s", err, out)
	}
	before := realHomeState()
	code := m.Run()
	if after := realHomeState(); after != before {
		return 0, fmt.Errorf("the tests changed the real home folder's agent config:\nbefore: %s\nafter:  %s", before, after)
	}
	return code, nil
}

// realHomeState fingerprints the real user's files that ctxtra setup
// would change, to catch a run that escaped its fixture.
func realHomeState() string {
	home, _ := os.UserHomeDir()
	var b strings.Builder
	for _, p := range []string{".claude/settings.json", ".codex/hooks.json", ".codex/config.toml",
		".pi/agent/extensions/ctxtra.ts", ".config/opencode/plugins/ctxtra.ts", ".cursor/hooks.json",
		".copilot/hooks/ctxtra.json", ".config/muse/settings.json",
		".gemini/config/hooks.json", ".gemini/antigravity-cli/settings.json",
		".hermes/plugins/ctxtra/__init__.py", ".hermes/config.yaml", ".dsh/ctxtra.mjs", ".dsh/cordis.patch.yml",
		".config/devin/config.json"} {
		st, err := os.Stat(filepath.Join(home, p))
		if err != nil {
			fmt.Fprintf(&b, "%s:none ", p)
			continue
		}
		fmt.Fprintf(&b, "%s:%d:%d ", p, st.Size(), st.ModTime().UnixNano())
	}
	return b.String()
}

// Agent is a driver with its resolved binary.
type Agent struct {
	driver.Driver
	Bin string
}

// forEachAgent runs f as a subtest for every agent that has all caps.
// An agent that lacks one is skipped with the reason; one that isn't
// installed is skipped, or fails with CTXTRA_E2E_REQUIRE=1.
func forEachAgent(t *testing.T, caps []driver.Cap, f func(t *testing.T, a Agent)) {
	for _, d := range driver.All {
		t.Run(d.Name(), func(t *testing.T) {
			if *agentFlag != "" && *agentFlag != d.Name() {
				t.Skip("-agent", *agentFlag)
			}
			for _, c := range caps {
				if !driver.Has(d, c) {
					t.Skipf("%s doesn't support %s", d.Name(), c)
				}
			}
			bin, err := driver.Binary(d)
			if err != nil {
				if os.Getenv("CTXTRA_E2E_REQUIRE") != "" {
					t.Fatal(err)
				}
				t.Skip(err)
			}
			t.Parallel()
			f(t, Agent{d, bin})
		})
	}
}

// Session is a fixture for one agent, with ctxtra set up for it.
type Session struct {
	*fixture.Fixture
	t     *testing.T
	agent Agent
	runs  int
}

func newSession(t *testing.T, a Agent) *Session {
	dir := ""
	if *keepFlag {
		var err error
		if dir, err = os.MkdirTemp("", "ctxtra-e2e-"+a.Name()+"-"); err != nil {
			t.Fatal(err)
		}
		t.Logf("home: %s", dir)
	}
	return &Session{Fixture: fixture.New(t, ctxtraBin, dir), t: t, agent: a}
}

// Setup runs `ctxtra setup` for the agent.
func (s *Session) Setup() { s.Ctxtra("setup", "--agent", s.agent.Name()) }

// Result is one agent run.
type Result struct {
	t        *testing.T
	Requests []mock.Request
	Stdout   string
	Stderr   string
}

// Run runs the agent in dir with the scripted steps and fails the test if
// the agent fails or the mock got no request.
func (s *Session) Run(dir string, opts driver.Options, script ...mock.Step) *Result {
	s.t.Helper()
	m := mock.New(s.t, script...)
	cmd, err := s.agent.Command(s.agent.Bin, s.Home, dir, m.URL(), mock.Prompt, opts, s.Env())
	if err != nil {
		s.t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), *timeoutRun)
	defer cancel()
	cmd = withContext(ctx, cmd)
	var stdout, stderr strings.Builder
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	start := time.Now()
	err = cmd.Run()
	r := &Result{t: s.t, Requests: m.Requests(), Stdout: stdout.String(), Stderr: stderr.String()}
	s.runs++
	run := s.runs
	s.t.Cleanup(func() { // before the fixture's TempDir is removed
		if s.t.Failed() {
			s.dump(r)
			s.saveEvidence(run, cmd, r)
		}
	})
	s.t.Logf("%s %s (%s): %s, %d model requests", s.agent.Name(), driver.Version(s.agent.Bin), s.agent.Bin,
		time.Since(start).Round(time.Millisecond), len(r.Requests))
	if err != nil || ctx.Err() != nil {
		s.t.Fatalf("%s run failed: %v (ctx: %v)", s.agent.Name(), err, ctx.Err())
	}
	if len(r.Requests) == 0 {
		s.t.Fatal("the mock got no model request")
	}
	if slices.Contains(script, mock.StartSubagent()) && len(r.View(mock.Subagent)) == 0 {
		s.t.Fatal("the script started a subagent, but the mock got no subagent request")
	}
	return r
}

// saveEvidence writes everything about a failed run to
// $CTXTRA_E2E_EVIDENCE/<test>/run<N>: the command, full output, every
// model request body, ctxtra's log, and the agent's own debug log if any.
// CI uploads that folder.
func (s *Session) saveEvidence(run int, cmd *exec.Cmd, r *Result) {
	root := os.Getenv("CTXTRA_E2E_EVIDENCE")
	if root == "" {
		return
	}
	dir := filepath.Join(root, strings.ReplaceAll(s.t.Name(), "/", "-"), fmt.Sprintf("run%d", run))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		s.t.Logf("evidence: %v", err)
		return
	}
	write := func(name string, b []byte) {
		if err := os.WriteFile(filepath.Join(dir, name), b, 0o644); err != nil {
			s.t.Logf("evidence: %v", err)
		}
	}
	command, _ := json.MarshalIndent(map[string]any{"args": cmd.Args, "dir": cmd.Dir, "env": cmd.Env,
		"version": driver.Version(s.agent.Bin)}, "", " ")
	write("command.json", command)
	write("stdout.txt", []byte(r.Stdout))
	write("stderr.txt", []byte(r.Stderr))
	reqs, _ := json.MarshalIndent(r.Requests, "", " ")
	write("requests.json", reqs)
	for _, name := range []string{".ctxtra/log.jsonl", "claude-debug.log"} {
		if b, err := os.ReadFile(s.Path(name)); err == nil {
			write(filepath.Base(name), b)
		}
	}
	s.t.Logf("evidence: %s", dir)
}

// withContext gives cmd a timeout that also kills its child processes.
func withContext(ctx context.Context, cmd *exec.Cmd) *exec.Cmd {
	c := exec.CommandContext(ctx, cmd.Path, cmd.Args[1:]...)
	c.Dir, c.Env, c.Stdin = cmd.Dir, cmd.Env, nil
	setProcessGroup(c)
	return c
}

func (s *Session) dump(r *Result) {
	var b strings.Builder
	fmt.Fprintf(&b, "stdout:\n%s\nstderr:\n%s\n", clip(r.Stdout, 6000), clip(r.Stderr, 6000))
	for i, q := range r.Requests {
		fmt.Fprintf(&b, "request %d: %s %s %s reply=%s markers=%v\n", i, q.API, q.Path, q.Role, q.Reply, mock.Markers(q.Text))
	}
	for _, e := range s.Log() {
		j, _ := json.Marshal(e)
		fmt.Fprintf(&b, "log: %s\n", j)
	}
	s.t.Log(b.String())
}

func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + fmt.Sprintf("\n... (%d more bytes)", len(s)-n)
}

// View is the requests of one role.
func (r *Result) View(role mock.Role) []mock.Request {
	var out []mock.Request
	for _, q := range r.Requests {
		if q.Role == role {
			out = append(out, q)
		}
	}
	return out
}

// First is the first request of role: what the agent had at startup.
func (r *Result) First(role mock.Role) Text {
	v := r.View(role)
	if len(v) == 0 {
		r.t.Fatalf("no %s request", role)
	}
	return Text{r.t, string(role) + "[first]", v[0].Text}
}

// Last is the last request of role: after the scripted tool calls.
func (r *Result) Last(role mock.Role) Text {
	v := r.View(role)
	if len(v) == 0 {
		r.t.Fatalf("no %s request", role)
	}
	return Text{r.t, string(role) + "[last]", v[len(v)-1].Text}
}

// Text is the model input of one request, with assertions on its markers.
type Text struct {
	t    *testing.T
	name string
	text string
}

// Has checks that every marker is present exactly once: a file ctxtra
// adds twice would cost tokens and can confuse the model.
func (x Text) Has(markers ...string) Text {
	x.t.Helper()
	for _, m := range markers {
		if n := mock.Count(x.text, m); n != 1 {
			x.t.Errorf("%s: CTXMARK-%s appears %d times, want 1 (markers: %v)", x.name, m, n, mock.Markers(x.text))
		}
	}
	return x
}

// Lacks checks that no marker is present.
func (x Text) Lacks(markers ...string) Text {
	x.t.Helper()
	for _, m := range markers {
		if mock.Count(x.text, m) > 0 {
			x.t.Errorf("%s: CTXMARK-%s is present, want absent (markers: %v)", x.name, m, mock.Markers(x.text))
		}
	}
	return x
}

// Order checks that the markers first appear in this order.
func (x Text) Order(markers ...string) Text {
	x.t.Helper()
	got := mock.Markers(x.text)
	var seen []string
	for _, m := range got {
		if slices.Contains(markers, m) {
			seen = append(seen, m)
		}
	}
	if !slices.Equal(seen, markers) {
		x.t.Errorf("%s: order %v, want %v (all markers: %v)", x.name, seen, markers, got)
	}
	return x
}

// Contains checks for a plain substring.
func (x Text) Contains(s string) Text {
	x.t.Helper()
	if !strings.Contains(x.text, s) {
		x.t.Errorf("%s: missing %q", x.name, s)
	}
	return x
}

// hookRan fails the test unless ctxtra's log has a run for one of the
// events (each agent names its events differently).
func (s *Session) hookRan(events ...string) {
	s.t.Helper()
	s.hookRanTimes(1, events...)
}

// hookRanTimes fails the test unless ctxtra's log has at least n runs of
// the events.
func (s *Session) hookRanTimes(n int, events ...string) {
	s.t.Helper()
	runs := 0
	for _, e := range s.Log() {
		if e.Harness == s.agent.Name() && slices.Contains(events, e.Event) && e.Error == "" {
			runs++
		}
	}
	if runs < n {
		s.t.Errorf("%d %s hook runs of %v in ctxtra's log, want %d or more: %+v", runs, s.agent.Name(), events, n, s.Log())
	}
}
