// Package codex supports Codex CLI: hooks in ~/.codex/hooks.json (or
// $CODEX_HOME). Codex runs a hook only after the user trusts it in /hooks,
// and asks again whenever the hook changes.
package codex

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/atelpis/ctxtra/internal/agent"
	"github.com/atelpis/ctxtra/internal/agent/hookjson"
	"github.com/atelpis/ctxtra/internal/chunk"
	"github.com/atelpis/ctxtra/internal/fsutil"
	"github.com/atelpis/ctxtra/internal/paths"
	"github.com/atelpis/ctxtra/internal/resolve"
)

// Agent is Codex CLI.
type Agent struct{}

var (
	_ agent.Agent    = Agent{}
	_ agent.Missing  = Agent{}
	_ agent.Retrust  = Agent{}
	_ agent.Native   = Agent{}
	_ agent.Launcher = Agent{}
)

// events are not registered for resume: the resumed transcript already
// contains the injected text. Changing them changes the hooks, and Codex
// then asks the user to trust them again.
var events = []hookjson.Event{{Name: "SessionStart", Matcher: "startup|clear|compact"}, {Name: "SubagentStart"}}

// trustNote tells the user how to trust the hooks.
const trustNote = "codex: Codex runs a new or changed hook only after you trust it, and asks once at its next start: " +
	"choose \"Trust all and continue\", or trust the ctxtra entries in /hooks."

// contextLimit is the hooks' additionalContextLimit, in tokens: Codex counts
// 4 bytes as a token, so 250,000 tokens are 1,000,000 bytes.
const contextLimit = 250_000

// minVersion is the first Codex version with additionalContextLimit. Older
// ones show the model only a preview of about 2,500 tokens.
const minVersion = "0.145.0"

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
		StatusMessage: "ctxtra: personal instructions", ContextLimit: contextLimit, Missing: a,
		RepairNote: " (" + a.TrustNote() + " again)"}
}

// NativeFiles: Codex reads AGENTS.override.md, or AGENTS.md when the
// override has no text.
func (Agent) NativeFiles() []agent.NativeFile {
	var out []agent.NativeFile
	if f, ok := agent.ReadNative(filepath.Join(dir(), "AGENTS.override.md")); ok {
		out = append(out, f)
	}
	if f, ok := agent.ReadNative(filepath.Join(dir(), "AGENTS.md")); ok {
		if len(out) > 0 {
			f.Note = "Codex doesn't read it while AGENTS.override.md has text"
		}
		out = append(out, f)
	}
	return out
}

func (Agent) Launch(prompt string) []string { return []string{"codex", prompt} }

func (Agent) TrustNote() string { return "Codex will then ask you to trust the hooks" }

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
// It also warns about a Codex too old for additionalContextLimit.
func (a Agent) Status(bin string, known bool) agent.Status {
	s := a.hooks().Status(bin, known)
	if v := version(); v != "" && older(v, minVersion) {
		s.Details = append(s.Details, fmt.Sprintf("WARNING: codex %s shows the model only about 10,000 characters of the text; update to %s or later", v, minVersion))
	}
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

// MissingAnswer: setup with the new binary changes the hooks, which Codex
// then asks the user to trust again.
func (a Agent) MissingAnswer(event, msg string) string {
	return agent.SystemMessage(event, msg+" ("+a.TrustNote()+" again)")
}

// Delivery: Codex gets the text in full up to the hook's
// additionalContextLimit, which it counts in bytes (measured with Codex
// 0.162.1).
func (Agent) Delivery() agent.Delivery {
	return agent.Delivery{Title: "Codex", Capacity: contextLimit * 4, Unit: chunk.Bytes}
}

// Check reads hooks.json only: Status also runs codex --version.
func (a Agent) Check(bin string, known bool) []string { return a.hooks().Check(bin, known) }

var reVersion = regexp.MustCompile(`\b(\d+)\.(\d+)\.(\d+)\b`)

// version is the version of the codex on PATH, or "" when there is none or
// it doesn't say. A variable for tests.
var version = func() string {
	bin, err := exec.LookPath("codex")
	if err != nil {
		return ""
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, bin, "--version").Output()
	if err != nil {
		return ""
	}
	return reVersion.FindString(string(out))
}

// older reports whether version a is older than b; both are X.Y.Z.
func older(a, b string) bool {
	x, y := reVersion.FindStringSubmatch(a), reVersion.FindStringSubmatch(b)
	if x == nil || y == nil {
		return false
	}
	for i := 1; i <= 3; i++ {
		m, _ := strconv.Atoi(x[i])
		n, _ := strconv.Atoi(y[i])
		if m != n {
			return m < n
		}
	}
	return false
}

var reUpper = regexp.MustCompile(`([a-z0-9])([A-Z])`)

func snake(s string) string { return strings.ToLower(reUpper.ReplaceAllString(s, "${1}_${2}")) }

var reStateHeader = regexp.MustCompile(`^\[hooks\.state\."((?:[^"\\]|\\.)*)"\]\s*(#.*)?$`)

// trustRecords returns the keys of [hooks.state."KEY"] tables in config.toml
// that have a trusted_hash. It reads only that simple layout, which is what
// Codex writes; comments are ignored.
func trustRecords(path string) []string {
	f, err := fsutil.OpenRegular(path, os.O_RDONLY, 0)
	if err != nil {
		return nil
	}
	defer f.Close()
	var out []string
	key := ""
	sc := bufio.NewScanner(io.LimitReader(f, agent.MaxConfig))
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
