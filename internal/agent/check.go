package agent

import (
	"fmt"
	"os"

	"github.com/atelpis/ctxtra/internal/paths"
)

// MaxConfig bounds what Check and Status read of an agent's file, so a huge
// file can't exhaust a command.
const MaxConfig = 16 << 20

// Repair is the command that sets up agent again, in a notice.
func Repair(agent string) string { return "run ctxtra setup --agent " + agent }

// BinaryNotices are Check's notices about the binaries bins that a setup
// runs, which what names ("the hooks run"): one that is gone, or that isn't
// the running binary bin ("" when it can't be compared). repair is the
// command that sets up the agent again.
func BinaryNotices(what string, bins []string, bin, repair string) []string {
	var out []string
	for _, b := range bins {
		switch st, err := os.Stat(b); {
		case err != nil:
			out = append(out, fmt.Sprintf("%s %s, which doesn't exist; %s", what, paths.Tilde(b), repair))
		case st.IsDir() || st.Mode()&0o111 == 0:
			out = append(out, fmt.Sprintf("%s %s, which isn't an executable file; %s", what, paths.Tilde(b), repair))
		case bin != "" && paths.Real(b) != paths.Real(bin):
			out = append(out, fmt.Sprintf("%s %s, not this ctxtra (%s); to switch, %s", what, paths.Tilde(b), paths.Tilde(bin), repair))
		}
	}
	return out
}

// LegacyNotice is Check's notice about a setup by an older version, which
// gets what old describes.
func LegacyNotice(old Delivery, repair string) string {
	return fmt.Sprintf("set up by an older ctxtra, with %s; %s", old.Over(), repair)
}

// GoneNotice is Check's notice about a setup that setup.json lists, but
// whose files no longer have it: what is gone from path.
func GoneNotice(what, path, agent, repair string) string {
	return fmt.Sprintf("%s gone from %s; if that wasn't on purpose, %s; if it was, ctxtra setup --remove --agent %s stops this notice",
		what, paths.Tilde(path), repair, agent)
}

// UnreadableNotice is Check's notice when it can't read path.
func UnreadableNotice(path string, err error) string {
	return fmt.Sprintf("can't check the setup in %s: %v", paths.Tilde(path), err)
}
