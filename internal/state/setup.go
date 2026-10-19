package state

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"

	"github.com/atelpis/ctxtra/internal/fsutil"
	"github.com/atelpis/ctxtra/internal/paths"
)

// Setups is setup.json: where setup set up each agent, and every agent it
// found on this machine. It is advisory: the notices after a command read
// it to tell a setup that is gone from one that never was, and to find new
// agents. It never decides a change of an agent's config.
type Setups struct {
	// Installed is the config paths where setup set up each agent, absolute.
	Installed map[string][]string `json:"installed"`
	Detected  []string            `json:"detected"`
}

// SetupsPath is ~/.ctxtra/setup.json.
func SetupsPath() string { return filepath.Join(paths.StateDir(), "setup.json") }

// SetupsLock is held for a whole setup run, its changes of agent configs
// and of setup.json, so the two can't disagree.
func SetupsLock() string { return filepath.Join(paths.StateDir(), "setup.lock") }

// LoadSetups reads setup.json. ok is false when there is none; a broken
// one is an error.
func LoadSetups() (s Setups, ok bool, err error) {
	b, err := fsutil.ReadBounded(SetupsPath(), maxBytes)
	if errors.Is(err, os.ErrNotExist) {
		return Setups{}, false, nil
	}
	if err == nil {
		err = json.Unmarshal(b, &s)
	}
	if err != nil {
		return Setups{}, false, fmt.Errorf("%s: %w", paths.Tilde(SetupsPath()), err)
	}
	return s, true, nil
}

// Known reports whether setup set up agent with its config at path.
func (s Setups) Known(agent, path string) bool {
	return slices.ContainsFunc(s.Installed[agent], func(p string) bool { return filepath.Clean(p) == abs(path) })
}

// abs is path as setup.json holds it.
func abs(path string) string {
	if p, err := filepath.Abs(path); err == nil {
		return p
	}
	return filepath.Clean(path)
}

// Seen reports whether setup set up agent, or found it.
func (s Setups) Seen(agent string) bool {
	return len(s.Installed[agent]) > 0 || slices.Contains(s.Detected, agent)
}

// Install records that setup set up agent with its config at path.
func (s *Setups) Install(agent, path string) {
	if s.Known(agent, path) {
		return
	}
	if s.Installed == nil {
		s.Installed = map[string][]string{}
	}
	s.Installed[agent] = append(s.Installed[agent], abs(path))
}

// Uninstall records that setup removed agent's setup at path.
func (s *Setups) Uninstall(agent, path string) {
	left := slices.DeleteFunc(s.Installed[agent], func(p string) bool { return filepath.Clean(p) == abs(path) })
	if len(left) == 0 {
		delete(s.Installed, agent)
	} else {
		s.Installed[agent] = left
	}
}

// Detect records that setup found agent.
func (s *Setups) Detect(agent string) {
	if !slices.Contains(s.Detected, agent) {
		s.Detected = append(s.Detected, agent)
	}
}

// WriteSetups writes setup.json atomically. The caller holds SetupsLock.
func WriteSetups(s Setups) error {
	if s.Installed == nil {
		s.Installed = map[string][]string{}
	}
	if s.Detected == nil {
		s.Detected = []string{}
	}
	return writeJSON(SetupsPath(), s)
}
