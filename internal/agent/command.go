package agent

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/atelpis/ctxtra/internal/paths"
)

// commandTemplate is the hook command: the binary path, the agent, the
// event with the hook's slot, and what it prints when the binary is gone, a
// shell word from missingWord: {}, or a message for the user. It reads stdin
// then, so the agent never gets a broken pipe; /bin/cat by its path, as PATH
// may be anything. Codex trusts a hook by a hash of the command, so the text
// must stay the same across versions.
const commandTemplate = `/bin/sh -c 'f="%s"; if [ -x "$f" ]; then exec "$f" hook %s %s; else /bin/cat >/dev/null 2>&1; echo %s; fi'`

// oldTemplates are earlier forms of commandTemplate, which printed {} when
// the binary was gone. Setup still recognizes them, so it updates such an
// entry in place instead of adding a second one.
var oldTemplates = []string{
	`/bin/sh -c 'f="%s"; if [ -x "$f" ]; then exec "$f" hook %s %s; else /bin/cat >/dev/null 2>&1; echo "{}"; fi'`,
	`/bin/sh -c 'f="%s"; if [ -x "$f" ]; then exec "$f" hook %s %s; else cat >/dev/null 2>&1; echo "{}"; fi'`,
}

// wordRe is a shell word that missingWord writes: a double-quoted string
// whose only escapes are \".
const wordRe = `"((?:[^"\\$` + "`" + `'\n]|\\")*)"`

// commandRes match commandTemplate and oldTemplates with any binary path, so
// setup recognizes its own entries also after the binary moved. The event
// can have a slot (SlotCommand), and a slot of an earlier version has no
// --of.
var commandRes = func() []*regexp.Regexp {
	var out []*regexp.Regexp
	for _, t := range append([]string{commandTemplate}, oldTemplates...) {
		p := strings.Split(t, "%s")
		re := "^" + regexp.QuoteMeta(p[0]) + `([^"]+)` + regexp.QuoteMeta(p[1]) + `(\S+)` + regexp.QuoteMeta(p[2]) +
			`(\S+)(?: --slot (\d+)(?: --of (\d+))?)?` + regexp.QuoteMeta(p[3])
		if len(p) == 5 {
			re += wordRe + regexp.QuoteMeta(p[4])
		}
		out = append(out, regexp.MustCompile(re+"$"))
	}
	return out
}()

// MissingMessage is what the hook of agent tells the user when the binary
// it runs is gone. It has no path: the hook command can't hold every path
// in a JSON string.
func MissingMessage(agent string) string {
	return "ctxtra: the ctxtra binary that this hook runs is missing; reinstall ctxtra, or run ctxtra setup --agent " +
		agent + " with the new binary"
}

// missingAnswer is what the hook of agent, event and slot prints when the
// binary is gone: m's message for slot 0, or for a hook without slots, and
// {} for the other slots and for an agent without a message.
func missingAnswer(m Missing, agent, event string, slot int) string {
	if m == nil || slot > 0 {
		return "{}"
	}
	if s := m.MissingAnswer(event, MissingMessage(agent)); s != "" && !strings.ContainsAny(s, "\\$`'\n") {
		return s
	}
	return "{}"
}

// missingWord is answer as a shell word inside commandTemplate's single
// quotes.
func missingWord(answer string) string { return `"` + strings.ReplaceAll(answer, `"`, `\"`) + `"` }

// Command is the hook command for one agent and event. A binary under the
// home folder is written as $HOME/..., so the same agent config works on
// another machine with a different user name. m is the agent, when its hook
// answer can show the user that the binary is gone.
func Command(bin, agent, event string, m Missing) string {
	return fmt.Sprintf(commandTemplate, ShellPath(bin), agent, event, missingWord(missingAnswer(m, agent, event, -1)))
}

// SlotCommand is the command of hook slot of the slots of a Slotted agent.
func SlotCommand(bin, agent, event string, slot, slots int, m Missing) string {
	return fmt.Sprintf(commandTemplate, ShellPath(bin), agent, fmt.Sprintf("%s --slot %d --of %d", event, slot, slots),
		missingWord(missingAnswer(m, agent, event, slot)))
}

// Hook is what one of our hook commands runs.
type Hook struct {
	Binary, Agent, Event string
	Slot                 int // --slot, or -1 without one
	Slots                int // --of, or 0 without one
}

// ParseCommand returns what cmd runs, and whether it is one of our hook
// commands: a form that setup writes or wrote, which prints {} when the
// binary is gone, or exactly the message that setup writes for m. Setup
// replaces and removes the entries that are ours, so a command edited by
// hand isn't one.
func ParseCommand(cmd string, m Missing) (Hook, bool) {
	for i, re := range commandRes {
		g := re.FindStringSubmatch(cmd)
		if g == nil {
			continue
		}
		h := Hook{Binary: ExpandShellPath(g[1]), Agent: g[2], Event: g[3], Slot: -1}
		if g[4] != "" {
			n, err := strconv.Atoi(g[4])
			if err != nil {
				return Hook{}, false
			}
			h.Slot = n
		}
		if g[5] != "" {
			n, err := strconv.Atoi(g[5])
			if err != nil {
				return Hook{}, false
			}
			h.Slots = n
		}
		if i == 0 {
			answer := strings.ReplaceAll(g[6], `\"`, `"`)
			if answer != "{}" && answer != missingAnswer(m, h.Agent, h.Event, h.Slot) {
				return Hook{}, false
			}
		}
		return h, true
	}
	return Hook{}, false
}

// ShellPath is bin as the hook command writes it.
func ShellPath(bin string) string {
	if t := paths.Tilde(bin); strings.HasPrefix(t, "~/") {
		return "$HOME/" + t[2:]
	}
	return bin
}

// ExpandShellPath turns a path written by ShellPath back into a file path.
func ExpandShellPath(p string) string {
	if rest, ok := strings.CutPrefix(p, "$HOME/"); ok {
		return filepath.Join(paths.Home(), rest)
	}
	return p
}

// CheckBinary is the state of the binary a hook runs, for Status: "ok" or
// "MISSING".
func CheckBinary(bin string) (string, bool) {
	if st, err := os.Stat(bin); err != nil || st.IsDir() || st.Mode()&0o111 == 0 {
		return "MISSING", false
	}
	return "ok", true
}

// ValidBinaryPath reports an error for a path the hook command can't hold.
func ValidBinaryPath(bin string) error {
	if !filepath.IsAbs(bin) {
		return fmt.Errorf("binary path %q is not absolute", bin)
	}
	if strings.ContainsAny(bin, "\"'$`\\\n") {
		return fmt.Errorf("unsupported characters in the binary path %q", bin)
	}
	return nil
}
