package cli

import (
	"fmt"
	"io"

	"github.com/atelpis/ctxtra/internal/agent"
	"github.com/atelpis/ctxtra/internal/agent/registry"
	"github.com/atelpis/ctxtra/internal/fsutil"
	"github.com/atelpis/ctxtra/internal/paths"
	"github.com/atelpis/ctxtra/internal/setup"
	"github.com/atelpis/ctxtra/internal/state"
)

// running is this binary, "" when it can't be compared; a variable for
// tests.
var running = setup.Running

// setupNotices tells, a line each, what the user should do about the
// agents' setups (agent.Agent.Check) and about agents found since the last
// setup, once each. It writes nothing but that record in setup.json, and
// never fails the command.
func setupNotices(w io.Writer) {
	setups, ok, err := state.LoadSetups()
	if err != nil {
		fmt.Fprintf(w, "%s is unreadable: notices about new or removed setups are off until the next ctxtra setup\n",
			paths.Tilde(state.SetupsPath()))
	}
	bin := running()
	var found []string
	for _, a := range registry.All() {
		for _, n := range a.Check(bin, setups.Known(a.Name(), a.ConfigPath())) {
			fmt.Fprintf(w, "%s: %s\n", a.Name(), n)
		}
		if ok && !setups.Seen(a.Name()) && a.Detect() {
			fmt.Fprintf(w, "%s: found since the last ctxtra setup; to add ctxtra to it, %s%s\n", a.Name(), agent.Repair(a.Name()), trustNote(a))
			found = append(found, a.Name())
		}
	}
	if len(found) > 0 {
		remember(found)
	}
}

// remember adds the agents to detected in setup.json, so their notice shows
// once. It doesn't wait for a setup that runs, and gives up without a word
// when it can't write: the notice then shows again.
func remember(agents []string) {
	unlock, err := fsutil.LockTimeout(state.SetupsLock(), 0)
	if err != nil {
		return
	}
	defer unlock()
	// Read again under the lock: a setup may have written it since.
	setups, ok, err := state.LoadSetups()
	if !ok || err != nil {
		return
	}
	for _, a := range agents {
		setups.Detect(a)
	}
	state.WriteSetups(setups)
}

// trustNote is what a notice adds when its command changes the hooks of
// an agent that asks to trust them.
func trustNote(a agent.Agent) string {
	if r, ok := a.(agent.Retrust); ok {
		return " (" + r.TrustNote() + ")"
	}
	return ""
}
