// Package mcpprobe is an experiment for the MCP proxy (CTX-31): it runs real
// agents against the mock model with ctxtra set up and a stdio MCP test
// server (./probe) registered, and records what the hook and the MCP server
// each see: the folder, the process chain, the MCP client capabilities and
// roots, and the MCP tool names that reach the model.
//
//	go test -count=1 ./mcpprobe -v -out DIR [-agents claude,codex]
//
// Each agent's home folder and a JSON record per scenario are kept in
// DIR/<agent>. Nothing is asserted: failures are data.
package mcpprobe

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/ctxtra/ctxtra/e2e/driver"
	"github.com/ctxtra/ctxtra/e2e/fixture"
	"github.com/ctxtra/ctxtra/e2e/mock"
)

var (
	outFlag    = flag.String("out", "", "results folder (required)")
	agentsFlag = flag.String("agents", "claude,codex,pi,opencode,muse,antigravity", "agents to run")
	scenFlag   = flag.String("scenarios", "top,sub,worktree,resume,resume-elsewhere,concurrent", "scenarios to run")
	runTimeout = flag.Duration("run-timeout", 3*time.Minute, "timeout of one agent run")
)

var ctxtraBin, probeBin string

func TestMain(m *testing.M) {
	flag.Parse()
	if *outFlag == "" {
		// A manual experiment, not part of go test ./...: it runs only with -out.
		fmt.Fprintln(os.Stderr, "mcpprobe: skipped without -out DIR")
		os.Exit(0)
	}
	dir := filepath.Join(*outFlag, "bin")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		panic(err)
	}
	ctxtraBin, probeBin = filepath.Join(dir, "ctxtra"), filepath.Join(dir, "probe")
	for _, b := range [][3]string{{"..", "./cmd/ctxtra", ctxtraBin}, {".", "./mcpprobe/probe", probeBin}} {
		cmd := exec.Command("go", "build", "-o", b[2], b[1])
		cmd.Dir = filepath.Join("..", b[0])
		if out, err := cmd.CombinedOutput(); err != nil {
			fmt.Fprintf(os.Stderr, "build %s: %v\n%s", b[1], err, out)
			os.Exit(1)
		}
	}
	os.Exit(m.Run())
}

// markers are the probe's tool names, or unique parts of them.
var markers = []string{"probe_ping", "linear-work__list_issues", "linear_work__list_issues", "dot.name", "dot_name", "colon:name", "colon_name",
	"L040", "L060", "L064", "L065", "L080", "L120", "late_added_tool"}

type env struct {
	t      *testing.T
	f      *fixture.Fixture
	name   string
	drv    driver.Driver
	bin    string
	mcpLog string
	hkLog  string
	out    string

	eager bool
	perm  bool // allow only the probe's linear-work__* tools, by a pattern

	mu       sync.Mutex
	claudeID map[string]string // dir -> last session id
}

func TestProbe(t *testing.T) {
	for _, name := range strings.Split(*agentsFlag, ",") {
		i := slices.IndexFunc(driver.All, func(d driver.Driver) bool { return d.Name() == name })
		if i < 0 {
			t.Fatalf("no driver %s", name)
		}
		d := driver.All[i]
		t.Run(name, func(t *testing.T) {
			bin, err := driver.Binary(d)
			if err != nil {
				t.Skip(err)
			}
			t.Parallel()
			out := filepath.Join(*outFlag, name)
			os.RemoveAll(out)
			if err := os.MkdirAll(filepath.Join(out, "home"), 0o755); err != nil {
				t.Fatal(err)
			}
			e := &env{t: t, f: fixture.New(t, ctxtraBin, filepath.Join(out, "home")), name: name, drv: d, bin: bin, out: out,
				claudeID: map[string]string{}}
			e.mcpLog, e.hkLog = e.f.Path("mcp-probe.jsonl"), e.f.Path("hook-probe.jsonl")
			e.prepare()
			for _, sc := range strings.Split(*scenFlag, ",") {
				e.scenario(sc)
			}
			e.saveJSON("ctxtra-log.json", e.f.Log())
		})
	}
}

// prepare builds the repos, the bindings, ctxtra's setup with the hook
// wrapped by the probe, and registers the MCP server.
func (e *env) prepare() {
	f := e.f
	f.Repo("dev/svc", "git@github.com:Acme/Svc.git", map[string]string{"sub/deeper/README.md": "README"})
	f.Repo("dev/other", "git@github.com:Other/Thing.git", map[string]string{"README.md": "OTHER"})
	f.Worktree("dev/svc", "elsewhere/svc-feature", "feature")
	for _, m := range []string{"REPO", "SUBDIR", "SUBPATH", "OTHERREPO", "GLOBAL"} {
		f.Marked("ctx/"+strings.ToLower(m)+".md", m)
	}
	f.Config(`schema: ctxtra/v1
bindings:
  - name: repo
    match: {repos: [{remote: "github.com/acme/**"}]}
    instructions: [~/ctx/repo.md]
  - name: subdir
    match: {dirs: [~/dev/svc/sub]}
    instructions: [~/ctx/subdir.md]
  - name: subpath
    match: {repos: [{remote: "github.com/acme/svc", subpath: "sub/**"}]}
    instructions: [~/ctx/subpath.md]
  - name: other
    match: {repos: [{remote: "github.com/other/**"}]}
    instructions: [~/ctx/otherrepo.md]
  - name: global
    match: {global: true}
    instructions: [~/ctx/global.md]
`)
	// The hooks run home/bin/ctxtra, a wrapper that records the hook's
	// process chain and input, then runs the real binary.
	real := f.Path("bin/ctxtra.real")
	copyFile(e.t, ctxtraBin, real)
	f.Write("bin/ctxtra", fmt.Sprintf("#!/bin/sh\nexec '%s' hook -log '%s' -real '%s' \"$@\"\n", probeBin, e.hkLog, real))
	os.Chmod(f.Path("bin/ctxtra"), 0o755)
	cmd := exec.Command(real, "setup", "--agent", e.name)
	cmd.Dir, cmd.Env = f.Home, f.Env()
	out, err := cmd.CombinedOutput()
	e.save("setup.txt", out)
	if err != nil {
		e.t.Fatalf("setup: %v\n%s", err, out)
	}
	e.rewriteHookPath()
	e.register()
}

// rewriteHookPath points the agent's hook config at the wrapper.
func (e *env) rewriteHookPath() {
	filepath.Walk(e.f.Home, func(p string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || info.Size() > 4<<20 || strings.Contains(p, "/dev/") || strings.Contains(p, "/bin/") {
			return nil
		}
		b, err := os.ReadFile(p)
		if err != nil || !strings.Contains(string(b), "ctxtra.real") {
			return nil
		}
		os.WriteFile(p, []byte(strings.ReplaceAll(string(b), "bin/ctxtra.real", "bin/ctxtra")), info.Mode())
		e.t.Logf("hook path rewritten in %s", p)
		return nil
	})
}

func (e *env) agentEnv() []string { return e.f.Env() }

// run runs a command in the fixture and records its output.
func (e *env) runCmd(label string, dir string, extraEnv []string, name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Env = append(e.agentEnv(), extraEnv...)
	out, err := cmd.CombinedOutput()
	e.save(label+".txt", []byte(fmt.Sprintf("$ %s %s\n%s\nerr: %v\n", name, strings.Join(args, " "), out, err)))
	return string(out), err
}

func (e *env) mcpArgs() []string { return []string{probeBin, "mcp", "-log", e.mcpLog} }

// register adds the probe as a user-level stdio MCP server, the way a user
// would.
func (e *env) register() {
	a := e.mcpArgs()
	switch e.name {
	case "claude":
		e.runCmd("register", e.f.Home, nil, e.bin, append([]string{"mcp", "add", "-s", "user", "probe", "--"}, a...)...)
	case "codex":
		e.runCmd("register", e.f.Home, nil, e.bin, append([]string{"mcp", "add", "probe", "--"}, a...)...)
	case "pi":
		e.runCmd("register", e.f.Home, nil, e.bin, append([]string{"mcp", "add", "probe", "--"}, a...)...)
		// Direct exposure: the tools are declared to the model, so their names can be seen.
		p := e.f.Path(".pi/agent/mcp.json")
		var cfg map[string]any
		if b, err := os.ReadFile(p); err == nil && json.Unmarshal(b, &cfg) == nil {
			if s, ok := cfg["mcpServers"].(map[string]any)["probe"].(map[string]any); ok {
				s["exposure"] = "direct"
			}
			b, _ = json.MarshalIndent(cfg, "", "  ")
			os.WriteFile(p, b, 0o644)
		}
	case "muse":
		p := e.f.Path(".config/muse/settings.json")
		cfg := map[string]any{}
		if b, err := os.ReadFile(p); err == nil {
			json.Unmarshal(b, &cfg)
		}
		if cfg["schema_version"] == nil {
			cfg["schema_version"] = 1
		}
		cfg["mcpServers"] = map[string]any{"probe": map[string]any{"type": "stdio", "command": a[0], "args": a[1:], "mode": "optional"}}
		b, _ := json.MarshalIndent(cfg, "", "  ")
		os.MkdirAll(filepath.Dir(p), 0o755)
		os.WriteFile(p, b, 0o644)
	}
	// OpenCode and Antigravity: perRun, after the driver rewrites their config.
}

func (e *env) perRun() {
	a := e.mcpArgs()
	switch e.name {
	case "opencode":
		e.runCmd("register", e.f.Home, nil, e.bin, append([]string{"mcp", "add", "--global", "probe", "--"}, a...)...)
	case "antigravity":
		e.runCmd("register", e.f.Home, nil, e.bin, append([]string{"mcp", "add", "probe", "--"}, a...)...)
		// Headless agy denies MCP calls without an allow rule.
		rule := "mcp(probe/*)"
		if e.perm {
			rule = "mcp(probe/linear-work__*)"
		}
		if r := os.Getenv("MCPPROBE_AGY_RULE"); r != "" && e.perm {
			rule = r
		}
		sp := e.f.Path(".gemini/antigravity-cli/settings.json")
		var st map[string]any
		if b, err := os.ReadFile(sp); err == nil && json.Unmarshal(b, &st) == nil {
			pm := st["permissions"].(map[string]any)
			pm["allow"] = append(pm["allow"].([]any), rule)
			b, _ = json.MarshalIndent(st, "", "  ")
			os.WriteFile(sp, b, 0o644)
		}
		if e.eager {
			p := e.f.Path(".gemini/config/mcp_config.json")
			var cfg map[string]any
			if b, err := os.ReadFile(p); err == nil && json.Unmarshal(b, &cfg) == nil {
				tools := map[string]any{}
				for _, n := range serverNames() {
					tools[n] = map[string]any{"eager": true}
				}
				cfg["mcpServers"].(map[string]any)["probe"].(map[string]any)["tools"] = tools
				b, _ = json.MarshalIndent(cfg, "", "  ")
				os.WriteFile(p, b, 0o644)
			}
		}
	}
	if e.name == "opencode" && e.eager { // tools sent directly, not through Code Mode
		p := e.f.Path(".config/opencode/opencode.json")
		var cfg map[string]any
		if b, err := os.ReadFile(p); err == nil && json.Unmarshal(b, &cfg) == nil {
			cfg["mcp"].(map[string]any)["servers"].(map[string]any)["probe"].(map[string]any)["codemode"] = false
			b, _ = json.MarshalIndent(cfg, "", "  ")
			os.WriteFile(p, b, 0o644)
		}
	}
	if e.name == "opencode" { // a second server with only plain tool names
		e.runCmd("register-plain", e.f.Home, nil, e.bin, "mcp", "add", "--global", "plain", "--", probeBin, "mcp",
			"-log", e.f.Path("plain-probe.jsonl"), "-names", "plain_ping,plain_two")
	}
}

// command is the driver's command, changed to allow the probe's tools and
// to keep sessions for resume.
func (e *env) command(dir, url, prompt string, resume bool, deferred bool) (*exec.Cmd, error) {
	cmd, err := e.drv.Command(e.bin, e.f.Home, dir, url, prompt, driver.Options{Prompt: prompt, Resume: resume && e.name == "muse"}, e.agentEnv())
	if err != nil {
		return nil, err
	}
	e.perRun()
	args := cmd.Args[1:]
	drop := func(flag string) { args = slices.DeleteFunc(args, func(s string) bool { return s == flag }) }
	insertBeforeLast := func(xs ...string) {
		last := args[len(args)-1]
		args = append(append(args[:len(args)-1:len(args)-1], xs...), last)
	}
	switch e.name {
	case "claude":
		if !deferred { // all MCP tools in the tool list, not behind ToolSearch
			cmd.Env = append(cmd.Env, "ENABLE_TOOL_SEARCH=false")
		}
		drop("--no-session-persistence")
		for i, a := range args {
			if a == "--allowedTools" {
				args[i+1] = "Read,Agent,mcp__probe"
				if e.perm {
					args[i+1] = "Read,Agent,mcp__probe__linear-work__*"
				}
			}
		}
		if resume {
			e.mu.Lock()
			id := e.claudeID["last"]
			e.mu.Unlock()
			args = append(args, "--resume", id)
		}
	case "codex":
		drop("--ephemeral")
		// Without it, exec refuses MCP calls: "MCP tool call requires approval, but approval policy is never".
		args = append([]string{args[0], "-c", `mcp_servers.probe.default_tools_approval_mode="approve"`}, args[1:]...)
		if resume {
			insertBeforeLast("resume", "--last")
		}
	case "muse":
		insertBeforeLast("--disable-approval")
	case "pi":
		drop("--no-session")
		if resume {
			insertBeforeLast("-c")
		}
	case "opencode":
		if resume {
			insertBeforeLast("--continue")
		}
	case "antigravity":
		if resume {
			args = append([]string{"--continue"}, args...)
		}
	}
	c := exec.Command(cmd.Path, args...)
	c.Dir, c.Env = cmd.Dir, cmd.Env
	return c, nil
}

// Run is one agent run and what it produced.
type Run struct {
	Label    string           `json:"label"`
	Dir      string           `json:"dir"`
	PID      int              `json:"pid"`
	Error    string           `json:"error,omitempty"`
	Duration string           `json:"duration"`
	Stdout   string           `json:"stdout_tail"`
	Stderr   string           `json:"stderr_tail"`
	Requests []RequestInfo    `json:"requests"`
	MCP      []map[string]any `json:"mcp"`
	Hooks    []map[string]any `json:"hooks"`
}

type RequestInfo struct {
	Role       string      `json:"role"`
	Reply      string      `json:"reply"`
	Tools      []mock.Tool `json:"probe_tools"`
	ToolCount  int         `json:"tool_count"`
	AllTools   []string    `json:"all_tools"`
	Mentions   []string    `json:"probe_mentions,omitempty"` // probe tool names in the text, not the tool list
	Markers    []string    `json:"markers"`
	ProbeTexts []string    `json:"probe_results,omitempty"`
}

var mentionRe = regexp.MustCompile(`[A-Za-z0-9_.:\-]*(probe_ping|linear-work|L0[0-9]{2}_|L1[0-9]{2}_|late_added|dot.name|colon.name)[A-Za-z0-9_.:\-]*`)

var resultRe = regexp.MustCompile(`PROBE-RESULT name=[^"\\]+`)

func (e *env) runAgent(label, dir string, resume bool, script ...mock.Step) *Run {
	m := mock.New(e.t, script...)
	cmd, err := e.command(dir, m.URL(), mock.Prompt, resume, label == "top-default")
	r := &Run{Label: label, Dir: dir}
	if err != nil {
		r.Error = err.Error()
		return r
	}
	ctx, cancel := context.WithTimeout(context.Background(), *runTimeout)
	defer cancel()
	c := exec.CommandContext(ctx, cmd.Path, cmd.Args[1:]...)
	c.Dir, c.Env = cmd.Dir, cmd.Env
	c.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	c.Cancel = func() error { return syscall.Kill(-c.Process.Pid, syscall.SIGKILL) }
	var stdout, stderr strings.Builder
	c.Stdout, c.Stderr = &stdout, &stderr
	start := time.Now()
	if err := c.Start(); err != nil {
		r.Error = err.Error()
		return r
	}
	r.PID = c.Process.Pid
	err = c.Wait()
	r.Duration = time.Since(start).Round(time.Millisecond).String()
	if err != nil {
		r.Error = err.Error()
	}
	time.Sleep(500 * time.Millisecond) // servers write their last records on stdin close
	r.Stdout, r.Stderr = tail(stdout.String(), 4000), tail(stderr.String(), 4000)
	e.saveJSON("requests-"+label+".json", m.Requests())
	for _, q := range m.Requests() {
		ri := RequestInfo{Role: string(q.Role), Reply: q.Reply, Markers: mock.Markers(q.Text)}
		all := mock.ToolNames(q.API, q.Body)
		ri.ToolCount = len(all)
		for _, tl := range all {
			ri.AllTools = append(ri.AllTools, tl.Namespace+"/"+tl.Name)
			if isProbe(tl.Name) {
				ri.Tools = append(ri.Tools, tl)
			}
		}
		ri.ProbeTexts = resultRe.FindAllString(q.Text+jsonString(q.Body), -1)
		slices.Sort(ri.ProbeTexts)
		ri.ProbeTexts = slices.Compact(ri.ProbeTexts)
		ri.Mentions = mentionRe.FindAllString(q.Text, -1)
		slices.Sort(ri.Mentions)
		ri.Mentions = slices.Compact(ri.Mentions)
		r.Requests = append(r.Requests, ri)
	}
	if e.name == "claude" {
		if id := regexp.MustCompile(`"session_id":"([^"]+)"`).FindStringSubmatch(stdout.String()); id != nil {
			e.mu.Lock()
			e.claudeID["last"] = id[1]
			e.mu.Unlock()
		}
	}
	return r
}

func isProbe(name string) bool {
	for _, m := range markers {
		if strings.Contains(name, m) {
			return true
		}
	}
	return strings.Contains(name, "probe")
}

func jsonString(v any) string { b, _ := json.Marshal(v); return string(b) }

func tail(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return "…" + s[len(s)-n:]
}

// attribute fills in the MCP and hook records whose process chain holds
// the run's agent process, and resets nothing: logs are read whole.
func (e *env) attribute(runs ...*Run) {
	mcp, hooks := readJSONL(e.mcpLog), readJSONL(e.hkLog)
	for _, r := range runs {
		r.MCP, r.Hooks = nil, nil
		owned := map[float64]bool{} // server pids with a start record in this run
		for _, rec := range mcp {
			if rec["event"] == "start" && hasAncestor(rec, r.PID) {
				owned[rec["pid"].(float64)] = true
			}
		}
		for _, rec := range mcp {
			if owned[rec["pid"].(float64)] {
				r.MCP = append(r.MCP, rec)
			}
		}
		for _, rec := range hooks {
			if hasAncestor(rec, r.PID) {
				r.Hooks = append(r.Hooks, rec)
			}
		}
	}
}

func hasAncestor(rec map[string]any, pid int) bool {
	as, _ := rec["ancestors"].([]any)
	for _, a := range as {
		if m, ok := a.(map[string]any); ok && int(m["pid"].(float64)) == pid {
			return true
		}
	}
	return false
}

func readJSONL(p string) []map[string]any {
	f, err := os.Open(p)
	if err != nil {
		return nil
	}
	defer f.Close()
	var out []map[string]any
	sc := bufio.NewScanner(f)
	sc.Buffer(nil, 16<<20)
	for sc.Scan() {
		var m map[string]any
		if json.Unmarshal(sc.Bytes(), &m) == nil {
			out = append(out, m)
		}
	}
	return out
}

// serverNames are the probe's tool names as it lists them.
func serverNames() []string {
	names := []string{"probe_ping", "linear-work__list_issues", "dot.name.tool", "colon:name:tool"}
	for _, n := range []int{40, 60, 64, 65, 80, 120} {
		m := fmt.Sprintf("L%03d_", n)
		names = append(names, m+strings.Repeat("x", n-len(m)))
	}
	return append(names, "late_added_tool")
}

// calls is the script step that calls the probe's tools the way the agent
// offers them.
func (e *env) calls(names ...string) mock.Step {
	switch e.name {
	case "muse":
		return mock.CallMCPToolsFlat(names...)
	case "antigravity":
		if e.eager {
			return mock.CallMCPTools(names...)
		}
		return mock.CallMCPGeneric("call_mcp_tool", "probe", names...)
	case "opencode":
		if e.eager {
			return mock.CallMCPTools(names...)
		}
		return mock.CallMCPCode("execute", "probe", names...)
	}
	return mock.CallMCPTools(names...)
}

func (e *env) scenario(sc string) {
	ping := e.calls("probe_ping")
	all := e.calls(markers...)
	if e.name == "antigravity" || e.name == "opencode" {
		all = e.calls(serverNames()...)
	}
	var runs []*Run
	switch sc {
	case "top":
		runs = append(runs, e.runAgent("top", e.f.Path("dev/svc"), false, all))
	case "top-default":
		runs = append(runs, e.runAgent("top-default", e.f.Path("dev/svc"), false, all))
	case "top-eager": // Antigravity with every probe tool eager
		e.eager = true
		// A file read first: OpenCode connects MCP servers after its first model request.
		runs = append(runs, e.runAgent("top-eager", e.f.Path("dev/svc"), false, mock.Read(e.f.Path("ctx/global.md")), e.calls(markers...)))
		e.eager = false
	case "perm": // which calls an allow pattern for linear-work__* lets through
		e.perm = true
		step := e.calls("linear-work", "linear_work", "probe_ping")
		if e.name == "antigravity" || e.name == "opencode" {
			step = e.calls("linear-work__list_issues", "probe_ping")
		}
		runs = append(runs, e.runAgent("perm", e.f.Path("dev/svc"), false, step))
		e.perm = false
	case "code-keys": // OpenCode: the names Code Mode gives the probe's tools
		runs = append(runs, e.runAgent("code-keys", e.f.Path("dev/svc"), false,
			mock.CallCode("execute", `return JSON.stringify({probe: Object.keys(tools.probe || {}), plain: Object.keys(tools.plain || {}), all: Object.keys(tools)})`)))
	case "sub":
		runs = append(runs, e.runAgent("sub", e.f.Path("dev/svc/sub/deeper"), false, ping))
	case "worktree":
		runs = append(runs, e.runAgent("worktree", e.f.Path("elsewhere/svc-feature"), false, ping))
	case "resume":
		runs = append(runs, e.runAgent("resume-first", e.f.Path("dev/svc"), false, ping))
		runs = append(runs, e.runAgent("resume", e.f.Path("dev/svc"), true, ping))
	case "resume-elsewhere":
		// A session started in dev/other, resumed from dev/svc/sub. For
		// agents whose resume looks up the last session by folder, this
		// resumes another session or none.
		runs = append(runs, e.runAgent("resume-elsewhere-first", e.f.Path("dev/other"), false, ping))
		runs = append(runs, e.runAgent("resume-elsewhere", e.f.Path("dev/svc/sub"), true, ping))
	case "concurrent":
		var wg sync.WaitGroup
		runs = make([]*Run, 2)
		for i, d := range []string{"dev/svc", "dev/other"} {
			wg.Add(1)
			go func() {
				defer wg.Done()
				runs[i] = e.runAgent(fmt.Sprintf("concurrent-%d", i), e.f.Path(d), false, ping)
			}()
		}
		wg.Wait()
	default:
		e.t.Fatalf("no scenario %s", sc)
	}
	e.attribute(runs...)
	for _, r := range runs {
		e.saveJSON(r.Label+".json", r)
		e.t.Logf("%s/%s: dir=%s err=%q requests=%d mcp_records=%d hook_records=%d", e.name, r.Label, r.Dir, r.Error,
			len(r.Requests), len(r.MCP), len(r.Hooks))
	}
}

func (e *env) save(name string, b []byte) { os.WriteFile(filepath.Join(e.out, name), b, 0o644) }

func (e *env) saveJSON(name string, v any) {
	b, _ := json.MarshalIndent(v, "", " ")
	e.save(name, b)
}

func copyFile(t *testing.T, from, to string) {
	b, err := os.ReadFile(from)
	if err != nil {
		t.Fatal(err)
	}
	os.MkdirAll(filepath.Dir(to), 0o755)
	if err := os.WriteFile(to, b, 0o755); err != nil {
		t.Fatal(err)
	}
}
