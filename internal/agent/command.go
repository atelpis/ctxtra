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

// commandTemplate is the hook command: the binary path, the agent and the
// event. It injects nothing when the binary is gone, and reads stdin so the
// agent never gets a broken pipe; /bin/cat by its path, as PATH may be
// anything. Codex trusts a hook by a hash of the command, so the text must
// stay the same across versions.
const commandTemplate = `/bin/sh -c 'f="%s"; if [ -x "$f" ]; then exec "$f" hook %s %s; else /bin/cat >/dev/null 2>&1; echo "{}"; fi'`

// oldTemplates are earlier forms of commandTemplate. Setup still recognizes
// them, so it updates such an entry in place instead of adding a second one.
var oldTemplates = []string{
	`/bin/sh -c 'f="%s"; if [ -x "$f" ]; then exec "$f" hook %s %s; else cat >/dev/null 2>&1; echo "{}"; fi'`,
}

// commandRes match commandTemplate and oldTemplates with any binary path, so
// setup recognizes its own entries also after the binary moved. The event
// can have a slot (SlotCommand).
var commandRes = func() []*regexp.Regexp {
	var out []*regexp.Regexp
	for _, t := range append([]string{commandTemplate}, oldTemplates...) {
		p := strings.Split(t, "%s")
		out = append(out, regexp.MustCompile("^"+regexp.QuoteMeta(p[0])+`([^"]+)`+regexp.QuoteMeta(p[1])+`(\S+)`+
			regexp.QuoteMeta(p[2])+`(\S+)(?: --slot (\d+))?`+regexp.QuoteMeta(p[3])+"$"))
	}
	return out
}()

// Command is the hook command for one agent and event. A binary under the
// home folder is written as $HOME/..., so the same agent config works on
// another machine with a different user name.
func Command(bin, agent, event string) string {
	return fmt.Sprintf(commandTemplate, ShellPath(bin), agent, event)
}

// SlotCommand is the command of hook slot of a Slotted agent.
func SlotCommand(bin, agent, event string, slot int) string {
	return Command(bin, agent, fmt.Sprintf("%s --slot %d", event, slot))
}

// CommandSlot returns the slot in one of our hook commands, and whether it
// has one.
func CommandSlot(cmd string) (int, bool) {
	for _, re := range commandRes {
		if m := re.FindStringSubmatch(cmd); m != nil && m[4] != "" {
			n, err := strconv.Atoi(m[4])
			return n, err == nil
		}
	}
	return 0, false
}

// ShellPath is bin as the hook command writes it.
func ShellPath(bin string) string {
	if t := paths.Tilde(bin); strings.HasPrefix(t, "~/") {
		return "$HOME/" + t[2:]
	}
	return bin
}

// CommandBinary returns the binary path in one of our hook commands, and
// whether cmd is one.
func CommandBinary(cmd string) (string, bool) {
	for _, re := range commandRes {
		if m := re.FindStringSubmatch(cmd); m != nil {
			return ExpandShellPath(m[1]), true
		}
	}
	return "", false
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
