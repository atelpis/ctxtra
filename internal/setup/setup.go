// Package setup adds ctxtra's hooks to the user-level configs of the
// supported agents, and removes them. Each agent changes only its own
// entries; see internal/agent.
package setup

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/atelpis/ctxtra/internal/agent"
	"github.com/atelpis/ctxtra/internal/agent/registry"
	"github.com/atelpis/ctxtra/internal/fsutil"
	"github.com/atelpis/ctxtra/internal/paths"
	"github.com/atelpis/ctxtra/internal/state"
)

// Options select what Run does.
type Options struct {
	Agents []string // empty: every agent found on this machine
	agent.Options
}

// Report is what Run changed, or would change with DryRun.
type Report struct {
	Agents []string
	agent.Report
	Errors []error // per agent; other agents are still handled
	// Record is why setup.json couldn't be written; the changes stay.
	Record error
}

// Run adds (or removes) the hooks for the selected agents.
func Run(o Options) (Report, error) {
	var agents []agent.Agent
	for _, name := range o.Agents {
		a, ok := registry.Get(name)
		if !ok {
			return Report{}, fmt.Errorf("unknown agent %q (one of: %s)", name, strings.Join(registry.Names(), ", "))
		}
		agents = append(agents, a)
	}
	if len(agents) == 0 {
		for _, a := range registry.All() {
			if a.Detect() {
				agents = append(agents, a)
			}
		}
	}
	if !o.Remove {
		if err := agent.ValidBinaryPath(o.Binary); err != nil {
			return Report{}, err
		}
	}
	if !o.DryRun {
		unlock, err := fsutil.Lock(state.SetupsLock())
		if err != nil {
			return Report{}, err
		}
		defer unlock()
	}
	var r Report
	var done []agent.Agent
	for _, a := range agents {
		r.Agents = append(r.Agents, a.Name())
		if err := a.Setup(o.Options, &r.Report); err != nil {
			r.Errors = append(r.Errors, fmt.Errorf("%s: %w", a.Name(), err))
			continue
		}
		done = append(done, a)
	}
	if !o.DryRun {
		r.Record = record(o.Remove, done)
	}
	return r, nil
}

// record adds to setup.json the agents that this run set up, or removes
// those it removed, and every agent found on this machine. A broken
// setup.json is replaced.
func record(remove bool, done []agent.Agent) error {
	s, _, err := state.LoadSetups()
	if err != nil {
		s = state.Setups{}
	}
	for _, a := range done {
		if remove {
			s.Uninstall(a.Name(), a.ConfigPath())
		} else {
			s.Install(a.Name(), a.ConfigPath())
		}
	}
	for _, a := range registry.All() {
		if a.Detect() {
			s.Detect(a.Name())
		}
	}
	return state.WriteSetups(s)
}

// Running is this binary, for comparing it with the binary that a setup
// runs; "" when it is in a temporary folder (go run, tests), where it is
// never the one that setup wrote.
func Running() string {
	exe, err := os.Executable()
	if err != nil || inTemp(exe) {
		return ""
	}
	return filepath.Clean(exe)
}

func inTemp(p string) bool {
	tmp := paths.Real(os.TempDir())
	return strings.HasPrefix(paths.Real(p), tmp+string(filepath.Separator))
}

// Binary is the path the hooks run: this executable as it is found in PATH,
// without resolving symlinks, so a package manager's link (for example
// /opt/homebrew/bin/ctxtra) keeps working after an upgrade. Warnings say
// when the hooks would run something other than `ctxtra` in PATH.
func Binary() (bin string, warnings []string, err error) {
	exe, err := os.Executable()
	if err != nil {
		return "", nil, err
	}
	exe = filepath.Clean(exe)
	if inTemp(exe) {
		return "", nil, errors.New("this ctxtra is in a temporary folder (go run?); install it first, for example with go install ./cmd/ctxtra")
	}
	bin = exe
	if found, err := exec.LookPath("ctxtra"); err != nil {
		warnings = append(warnings, fmt.Sprintf("ctxtra is not in PATH; the hooks will run %s. Run setup again if you move it.", exe))
	} else if found, err = filepath.Abs(found); err == nil {
		if paths.Real(found) == paths.Real(exe) {
			bin = found
		} else {
			warnings = append(warnings, fmt.Sprintf("the hooks will run %s, but ctxtra in PATH is %s", exe, found))
		}
	}
	if err := agent.ValidBinaryPath(bin); err != nil {
		return "", nil, err
	}
	return bin, warnings, nil
}
