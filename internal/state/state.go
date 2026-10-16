// Package state keeps what ctxtra learns about pinned sources on this
// machine: when each was last checked for new commits, and the commit the
// user didn't accept. None of it changes what a session gets: losing the
// file only loses the notices.
package state

import (
	"encoding/json"
	"path/filepath"
	"time"

	"github.com/atelpis/ctxtra/internal/fsutil"
	"github.com/atelpis/ctxtra/internal/paths"
)

const maxBytes = 1 << 20

// Source is the state of one pinned source, by its store ID and branch. URL
// and Branch tell whether the entry is still about the source in the
// config.
type Source struct {
	URL       string    `json:"url"`
	Branch    string    `json:"branch"`
	LastCheck time.Time `json:"lastCheck,omitzero"`  // the last fetch of the branch that worked
	Dismissed string    `json:"dismissed,omitempty"` // the last commit the user didn't accept
}

// State is the whole file.
type State struct {
	Sources map[string]Source `json:"sources"`
}

// Path is ~/.ctxtra/state.json.
func Path() string { return filepath.Join(paths.StateDir(), "state.json") }

// Load reads the state. A missing or broken file is an empty state.
func Load() State {
	var s State
	if b, err := fsutil.ReadBounded(Path(), maxBytes); err == nil {
		if json.Unmarshal(b, &s) != nil {
			s = State{} // not partly decoded
		}
	}
	if s.Sources == nil {
		s.Sources = map[string]Source{}
	}
	return s
}

// key is per store ID and branch: two sources can follow two branches of
// one repository.
func key(id, branch string) string { return id + "@" + branch }

// Get is the state of the source id that follows branch, if it is about
// url.
func (s State) Get(id, url, branch string) Source {
	e := s.Sources[key(id, branch)]
	if e.URL != url || e.Branch != branch {
		return Source{URL: url, Branch: branch}
	}
	return e
}

// Update changes the entry of the source id under a lock and writes the
// file atomically.
func Update(id, url, branch string, change func(*Source)) error {
	unlock, err := fsutil.Lock(filepath.Join(paths.StateDir(), "state.lock"))
	if err != nil {
		return err
	}
	defer unlock()
	s := Load()
	e := s.Get(id, url, branch)
	change(&e)
	s.Sources[key(id, branch)] = e
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return fsutil.WriteFile(Path(), append(b, '\n'), 0o600)
}
