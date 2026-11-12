package e2e

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/ctxtra/ctxtra/e2e/driver"
)

// OpenCode's shared background service: sessions in a repository's
// subfolder and in a worktree outside the repository, served by one
// service, each get their own proxy, started in their folder, with that
// folder's servers.
func TestMCPOpenCodeService(t *testing.T) {
	forEachAgent(t, nil, func(t *testing.T, a Agent) {
		if a.Name() != "opencode" {
			t.Skip("OpenCode's shared service")
		}
		probe := probeBinary(t)
		s := mcpSession(t, a, func(s *Session) string {
			return `schema: ctxtra/v1
mcp-servers:
  alpha: {command: [` + probe + `, mcp, -log, ` + s.Path("alpha.jsonl") + `]}
  beta: {command: [` + probe + `, mcp, -log, ` + s.Path("beta.jsonl") + `]}
bindings:
  - name: svc
    match: {repos: [{remote: github.com/acme/svc}]}
    mcp-servers: [alpha]
  - name: billing
    match: {repos: [{remote: github.com/acme/mono, subpath: services/billing/**}]}
    mcp-servers: [beta]
`
		})
		s.Repo("dev/svc", "git@github.com:Acme/Svc.git", map[string]string{"README.md": "SVC"})
		wt := s.Worktree("dev/svc", "elsewhere/svc-feature", "feature")
		s.Repo("dev/mono", "git@github.com:Acme/Mono.git", map[string]string{"services/billing/README.md": "BILLING"})
		billing := s.Path("dev/mono/services/billing")

		// A service of its own: its own port, in the test's home; stopped
		// at the end.
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		port := ln.Addr().(*net.TCPAddr).Port
		ln.Close()
		serviceEnv := s.Env()
		service := func(args ...string) (string, error) {
			ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
			defer cancel()
			cmd := exec.CommandContext(ctx, a.Bin, append([]string{"service"}, args...)...)
			cmd.Dir, cmd.Env = s.Home, serviceEnv
			out, err := cmd.CombinedOutput()
			return string(out), err
		}
		if out, err := service("set", "port", strconv.Itoa(port)); err != nil {
			t.Fatalf("opencode service set port: %v\n%s", err, out)
		}
		// Wait for the service and its proxies before removing the home,
		// even if stop fails. Kill this test's service if it still listens.
		t.Cleanup(func() {
			out, err := service("stop")
			for i := 0; listening(port); i++ {
				if i == 50 {
					t.Errorf("OpenCode's service still listens on port %d: killed; opencode service stop: %v\n%s", port, err, out)
					for _, pid := range services(s.Home) {
						_ = syscall.Kill(-pid, syscall.SIGKILL)
						_ = syscall.Kill(pid, syscall.SIGKILL)
					}
					break
				}
				time.Sleep(200 * time.Millisecond)
			}
		})
		adjust := s.Adjust
		started := false
		s.Adjust = func(cmd *exec.Cmd) {
			adjust(cmd)
			cmd.Args = slices.DeleteFunc(cmd.Args, func(a string) bool { return a == "--standalone" })
			if !started {
				// Start after the driver writes the mock provider and setup adds MCP.
				serviceEnv = cmd.Env
				if out, err := service("start"); err != nil {
					t.Fatalf("opencode service start: %v\n%s", err, out)
				}
				started = true
			}
		}

		s.Run(wt, driver.Options{}, mcpScript(a.Name(), "alpha__probe_ping")...)
		s.Run(billing, driver.Options{}, mcpScript(a.Name(), "beta__probe_ping")...)
		// One service served both folders: OpenCode's log has one start.
		if n := serviceStarts(t, s.Path(".local/share/opencode/log")); n != 1 {
			t.Errorf("OpenCode started its service %d times, want 1", n)
		}
		if calls, _ := probeCalls(t, s.Path("alpha.jsonl")); !slices.Contains(calls, "probe_ping") {
			t.Errorf("alpha got calls %v from the worktree", calls)
		}
		if calls, _ := probeCalls(t, s.Path("beta.jsonl")); !slices.Contains(calls, "probe_ping") {
			t.Errorf("beta got calls %v from the subfolder", calls)
		}
		// The runs went through the service, which still runs.
		if st, _ := service("status"); !strings.Contains(st, strconv.Itoa(port)) {
			t.Errorf("the service isn't running on port %d:\n%s", port, st)
		}
		dirs := proxyDirs(t, s.Path(".ctxtra/log.jsonl"))
		for _, want := range []string{realPath(wt), realPath(billing)} {
			if !slices.Contains(dirs, want) {
				t.Errorf("no proxy started in %s; started in %v", want, dirs)
			}
		}
	})
}

// proxyDirs are the folders the proxies in log started in.
func proxyDirs(t *testing.T, log string) []string {
	t.Helper()
	f, err := os.Open(log)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var dirs []string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		var rec struct{ Kind, Event, Dir string }
		if json.Unmarshal(sc.Bytes(), &rec) == nil && rec.Kind == "mcp" && rec.Event == "start" {
			dirs = append(dirs, realPath(rec.Dir))
		}
	}
	return dirs
}

func realPath(p string) string {
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r
	}
	return p
}

func listening(port int) bool {
	c, err := net.DialTimeout("tcp", "127.0.0.1:"+strconv.Itoa(port), time.Second)
	if err != nil {
		return false
	}
	c.Close()
	return true
}

// serviceStarts counts how often OpenCode logged starting its service.
func serviceStarts(t *testing.T, dir string) int {
	t.Helper()
	files, _ := filepath.Glob(filepath.Join(dir, "*.log"))
	n := 0
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, line := range strings.Split(string(data), "\n") {
			if strings.Contains(line, `message="cli starting"`) && strings.Contains(line, `\"serve\",\"--service\"`) {
				n++
			}
		}
	}
	return n
}

// services are the OpenCode service processes of the test's home: on
// Linux from /proc, elsewhere from ps.
func services(home string) []int {
	var pids []int
	if entries, err := os.ReadDir("/proc"); err == nil {
		for _, e := range entries {
			pid, err := strconv.Atoi(e.Name())
			if err != nil {
				continue
			}
			cmd, _ := os.ReadFile(filepath.Join("/proc", e.Name(), "cmdline"))
			env, _ := os.ReadFile(filepath.Join("/proc", e.Name(), "environ"))
			if bytes.Contains(cmd, []byte("--service")) && bytes.Contains(env, []byte("HOME="+home+"\x00")) {
				pids = append(pids, pid)
			}
		}
		return pids
	}
	out, _ := exec.Command("ps", "-Eaxo", "pid=,command=").Output()
	for _, line := range strings.Split(string(out), "\n") {
		f := strings.Fields(line)
		if len(f) > 1 && strings.Contains(line, "serve --service") && strings.Contains(line, "HOME="+home+" ") {
			if pid, err := strconv.Atoi(f[0]); err == nil {
				pids = append(pids, pid)
			}
		}
	}
	return pids
}
