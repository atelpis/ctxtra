package main

// CLI integration tests: each file in testdata/script is a testscript
// (https://pkg.go.dev/github.com/rogpeppe/go-internal/testscript) that runs
// the real ctxtra binary in an isolated HOME. They need no agent: runhook
// runs the hook command that setup wrote, the way the agent would.
//
// Run one script: go test ./cmd/ctxtra -run TestScript/setup_claude

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/rogpeppe/go-internal/testscript"

	"github.com/atelpis/ctxtra/internal/testenv"
)

func TestMain(m *testing.M) {
	testscript.Main(m, map[string]func(){"ctxtra": main})
}

func TestScript(t *testing.T) {
	testscript.Run(t, testscript.Params{
		Dir:                 "testdata/script",
		Setup:               setupEnv,
		RequireExplicitExec: true,
		RequireUniqueNames:  true,
		Cmds: map[string]func(*testscript.TestScript, bool, []string){
			"runhook":   runHook,
			"gitrepo":   gitRepo,
			"gitcommit": gitCommit,
			"expand":    expandFile,
		},
	})
}

// setupEnv makes the script's work folder its HOME, by its path without
// symlinks (macOS temp folders are under a symlink, and git reports real
// paths), so the files in the script are relative to HOME. Git
// reads no user or system config. $BIN is the binary the hooks run.
func setupEnv(e *testscript.Env) error {
	work, err := filepath.EvalSymlinks(e.WorkDir)
	if err != nil {
		return err
	}
	home := work
	bin := ""
	for _, d := range filepath.SplitList(e.Getenv("PATH")) {
		if p := filepath.Join(d, "ctxtra"); isFile(p) {
			bin = p
			break
		}
	}
	if bin == "" {
		return fmt.Errorf("ctxtra not found in PATH %q", e.Getenv("PATH"))
	}
	// $GITSRV serves the repositories in $WORK/srv over https, for the
	// sources tests: ctxtra fetches only over https and ssh.
	srv, stop, err := testenv.StartGitServer(filepath.Join(work, "srv"))
	if err != nil {
		return err
	}
	e.Defer(stop)
	for k, v := range map[string]string{
		"HOME": home, "BIN": bin, "GITSRV": srv, "GIT_SSL_NO_VERIFY": "1",
		"GIT_CONFIG_GLOBAL": os.DevNull, "GIT_CONFIG_NOSYSTEM": "1",
		"GIT_AUTHOR_NAME": "t", "GIT_AUTHOR_EMAIL": "t@example.com",
		"GIT_COMMITTER_NAME": "t", "GIT_COMMITTER_EMAIL": "t@example.com",
	} {
		e.Setenv(k, v)
	}
	e.Cd = home
	return nil
}

func isFile(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.Mode().IsRegular()
}

// gitrepo DIR [REMOTE]: a git repository in DIR with everything in it
// committed, and an origin remote if REMOTE is set.
func gitRepo(ts *testscript.TestScript, neg bool, args []string) {
	if neg || len(args) < 1 || len(args) > 2 {
		ts.Fatalf("usage: gitrepo DIR [REMOTE]")
	}
	dir := ts.MkAbs(args[0])
	ts.Check(os.MkdirAll(dir, 0o755))
	cmds := [][]string{{"init", "-q", "-b", "main"}, {"add", "-A"}, {"commit", "-q", "--allow-empty", "-m", "init"}}
	if len(args) == 2 {
		cmds = append(cmds, []string{"remote", "add", "origin", args[1]})
	}
	for _, c := range cmds {
		git(ts, dir, c...)
	}
}

// gitcommit DIR: commits everything in the repository in DIR.
func gitCommit(ts *testscript.TestScript, neg bool, args []string) {
	if neg || len(args) != 1 {
		ts.Fatalf("usage: gitcommit DIR")
	}
	dir := ts.MkAbs(args[0])
	git(ts, dir, "add", "-A")
	git(ts, dir, "commit", "-q", "-m", "change")
}

func git(ts *testscript.TestScript, dir string, args ...string) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = environ(ts)
	if out, err := cmd.CombinedOutput(); err != nil {
		ts.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
}

// environ is the script's environment, for commands that custom commands run.
func environ(ts *testscript.TestScript) []string {
	var env []string
	for _, k := range []string{"PATH", "HOME", "TMPDIR", "GIT_CONFIG_GLOBAL", "GIT_CONFIG_NOSYSTEM",
		"GIT_AUTHOR_NAME", "GIT_AUTHOR_EMAIL", "GIT_COMMITTER_NAME", "GIT_COMMITTER_EMAIL",
		"CTXTRA_CONFIG", "CTXTRA_LOG", "CLAUDE_CONFIG_DIR", "CODEX_HOME", "PI_CODING_AGENT_DIR",
		"OPENCODE_CONFIG_DIR", "XDG_CONFIG_HOME", "HERMES_HOME", "DSH_HOME", "GIT_SSL_NO_VERIFY"} {
		if v := ts.Getenv(k); v != "" {
			env = append(env, k+"="+v)
		}
	}
	return env
}

// expand SRC DST: copies SRC to DST with $VAR and ${VAR} replaced by the
// script's environment, like cmpenv does for comparisons.
func expandFile(ts *testscript.TestScript, neg bool, args []string) {
	if neg || len(args) != 2 {
		ts.Fatalf("usage: expand SRC DST")
	}
	dst := ts.MkAbs(args[1])
	ts.Check(os.MkdirAll(filepath.Dir(dst), 0o755))
	ts.Check(os.WriteFile(dst, []byte(os.Expand(ts.ReadFile(args[0]), ts.Getenv)), 0o644))
}

// runhook AGENT EVENT DIR: runs the hook command that setup wrote into the
// agent's config for EVENT, with the hook input the agent sends for a
// session in DIR, and makes its output available to stdout/stderr. For Pi,
// OpenCode, Hermes and DSH, whose extensions aren't shell commands, it runs
// the binary named in the extension the way the extension does. Antigravity
// gets its own input and working folder.
func runHook(ts *testscript.TestScript, neg bool, args []string) {
	if len(args) != 3 {
		ts.Fatalf("usage: runhook AGENT EVENT DIR")
	}
	agent, event, dir := args[0], args[1], ts.MkAbs(args[2])
	home := ts.Getenv("HOME")
	input, _ := json.Marshal(map[string]string{"cwd": dir, "hook_event_name": event, "source": "startup"})
	var cmd *exec.Cmd
	switch agent {
	case "claude", "codex", "copilot", "muse":
		cfg := map[string]string{"claude": ".claude/settings.json", "codex": ".codex/hooks.json",
			"copilot": ".copilot/hooks/ctxtra.json", "muse": ".config/muse/settings.json"}[agent]
		cmd = exec.Command("/bin/sh", "-c", hookCommand(ts, filepath.Join(home, cfg), event))
	case "antigravity":
		// No cwd in the input, and the hook runs in the hook file's folder.
		cfg := filepath.Join(home, ".gemini/config/hooks.json")
		var hooks struct {
			Ctxtra struct{ SessionStart []struct{ Command string } }
		}
		ts.Check(json.Unmarshal([]byte(ts.ReadFile(cfg)), &hooks))
		if n := len(hooks.Ctxtra.SessionStart); n != 1 {
			ts.Fatalf("%s: want one ctxtra SessionStart hook, found %d", cfg, n)
		}
		input, _ = json.Marshal(map[string]any{"workspacePaths": []string{dir}, "conversationId": "e2e"})
		cmd = exec.Command("/bin/sh", "-c", hooks.Ctxtra.SessionStart[0].Command)
		cmd.Dir = filepath.Dir(cfg)
	case "pi", "opencode", "hermes", "dsh":
		path := map[string]string{"pi": ".pi/agent/extensions/ctxtra.ts", "opencode": ".config/opencode/plugins/ctxtra.ts",
			"hermes": ".hermes/plugins/ctxtra/__init__.py", "dsh": ".dsh/ctxtra.mjs"}[agent]
		m := regexp.MustCompile(`(?m)^(?://|#) binary: (.+)$`).FindStringSubmatch(ts.ReadFile(filepath.Join(home, path)))
		if m == nil {
			ts.Fatalf("no binary in %s", path)
		}
		bin := strings.Replace(m[1], "$HOME", home, 1)
		input, _ = json.Marshal(map[string]string{"cwd": dir})
		cmd = exec.Command(bin, "hook", agent, event)
	default:
		ts.Fatalf("runhook: unknown agent %q", agent)
	}
	if cmd.Dir == "" {
		cmd.Dir = home
	}
	cmd.Env = environ(ts)
	if agent == "muse" {
		// Muse runs hooks with a cleared environment: HOME and PATH, but
		// none of the CTXTRA_ or git variables.
		cmd.Env = []string{"HOME=" + home, "PATH=" + ts.Getenv("PATH")}
	}
	cmd.Stdin = strings.NewReader(string(input))
	var stdout, stderr strings.Builder
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	fmt.Fprint(ts.Stdout(), stdout.String())
	fmt.Fprint(ts.Stderr(), stderr.String())
	if (err != nil) != neg {
		ts.Fatalf("runhook %s %s: unexpected result: %v", agent, event, err)
	}
}

// hookCommand is the one ctxtra command among the hooks for event in a
// Claude, Codex or Muse hook file, or a flat one (Copilot) where each entry
// is a handler.
func hookCommand(ts *testscript.TestScript, path, event string) string {
	type handler struct{ Command string }
	var cfg struct {
		Hooks map[string][]struct {
			handler
			Hooks []handler
		}
	}
	ts.Check(json.Unmarshal([]byte(ts.ReadFile(path)), &cfg))
	var found []string
	for _, g := range cfg.Hooks[event] {
		for _, h := range append(g.Hooks, g.handler) {
			if strings.Contains(h.Command, " hook ") && strings.Contains(h.Command, "ctxtra") {
				found = append(found, h.Command)
			}
		}
	}
	if len(found) != 1 {
		ts.Fatalf("%s: want one ctxtra %s hook, found %d", path, event, len(found))
	}
	return found[0]
}
