// Package codex supports Codex CLI: hooks in ~/.codex/hooks.json (or
// $CODEX_HOME). Codex runs a hook only after the user trusts it in /hooks,
// and asks again whenever the hook changes.
package codex

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/atelpis/ctxtra/internal/agent"
	"github.com/atelpis/ctxtra/internal/agent/hookjson"
	"github.com/atelpis/ctxtra/internal/paths"
	"github.com/atelpis/ctxtra/internal/resolve"
)

// Agent is Codex CLI.
type Agent struct{}

var _ agent.Agent = Agent{}

// events are not registered for resume: the resumed transcript already
// contains the injected text. Changing them changes the hooks, and Codex
// then asks the user to trust them again.
var events = []hookjson.Event{{Name: "SessionStart", Matcher: "startup|clear|compact"}, {Name: "SubagentStart"}}

// trustNote tells the user how to trust the hooks.
const trustNote = "codex: hooks run only after you trust them: start `codex`, run /hooks, and trust the ctxtra entries. Codex asks again after every change to a hook."

func (Agent) Name() string { return "codex" }

func dir() string {
	if d := os.Getenv("CODEX_HOME"); d != "" {
		return d
	}
	return filepath.Join(paths.Home(), ".codex")
}

func (Agent) ConfigPath() string { return filepath.Join(dir(), "hooks.json") }

func (Agent) Detect() bool {
	if st, err := os.Stat(dir()); err == nil && st.IsDir() {
		return true
	}
	_, err := exec.LookPath("codex")
	return err == nil
}

func (a Agent) hooks() hookjson.Config {
	// The status message names the hook in Codex's /hooks review.
	return hookjson.Config{Agent: a.Name(), Path: a.ConfigPath(), Events: events,
		StatusMessage: "ctxtra: personal instructions"}
}

// Setup also asks the user to review the hooks whenever it changed them:
// Codex keeps an old trust record but no longer runs a changed hook.
func (a Agent) Setup(o agent.Options, r *agent.Report) error {
	before := len(r.Changes)
	err := a.hooks().Apply(o, r)
	if len(r.Changes) > before && !o.Remove {
		r.Notes = append(r.Notes, trustNote)
	}
	return err
}

// Status lists a trust record per hook. Codex keys trust by position and
// stores a hash whose format isn't documented, so a record shows that the
// user trusted a hook at that position once, not that it is still current.
func (a Agent) Status() agent.Status {
	s := a.hooks().Status()
	cmds, _ := a.hooks().Commands()
	records := trustRecords(filepath.Join(dir(), "config.toml"))
	for _, ev := range events {
		for _, c := range cmds[ev.Name] {
			key := fmt.Sprintf("%s:%s:%d:%d", a.ConfigPath(), snake(ev.Name), c.Group, c.Handler)
			state := "trust record present (if you changed the hook since, trust it again in /hooks)"
			if !slices.Contains(records, key) {
				state = "NOT TRUSTED (run /hooks in codex)"
				s.OK = false
			}
			s.Details = append(s.Details, fmt.Sprintf("trust %s: %s", key, state))
		}
	}
	return s
}

func (Agent) Answer(event string, res *resolve.Result) any {
	return agent.AdditionalContext(event, res)
}

// PreviewLimit: about 2,500 tokens per Codex docs, at about 4 characters per
// token.
func (Agent) PreviewLimit() int { return 10000 }

var reUpper = regexp.MustCompile(`([a-z0-9])([A-Z])`)

func snake(s string) string { return strings.ToLower(reUpper.ReplaceAllString(s, "${1}_${2}")) }

var reStateHeader = regexp.MustCompile(`^\[hooks\.state\."((?:[^"\\]|\\.)*)"\]\s*(#.*)?$`)

// trustRecords returns the keys of [hooks.state."KEY"] tables in config.toml
// that have a trusted_hash. It reads only that simple layout, which is what
// Codex writes; comments are ignored.
func trustRecords(path string) []string {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	var out []string
	key := ""
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		switch {
		case strings.HasPrefix(line, "["):
			key = ""
			if m := reStateHeader.FindStringSubmatch(line); m != nil {
				key = strings.ReplaceAll(strings.ReplaceAll(m[1], `\"`, `"`), `\\`, `\`)
			}
		case key != "" && strings.HasPrefix(line, "trusted_hash"):
			if k, _, ok := strings.Cut(line, "="); ok && strings.TrimSpace(k) == "trusted_hash" {
				out = append(out, key)
				key = ""
			}
		}
	}
	return out
}
