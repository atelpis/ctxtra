// Package state keeps what ctxtra learns on this machine.
//
// state.json holds when each pinned source was last checked for new
// commits, and the commit the user didn't accept. None of it changes what a
// session gets: losing it loses the notices.
//
// mappings.json holds the folders of imported clones (see Mapping), and
// configs.json the config files add and sync used. They change what a
// session gets, so they are kept apart, and a broken file is reported,
// never rewritten.
package state

import (
	"encoding/json"
	"fmt"
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
	return update(func(s *State) error {
		e := s.Get(id, url, branch)
		change(&e)
		s.Sources[key(id, branch)] = e
		return nil
	})
}

func update(change func(*State) error) error {
	unlock, err := fsutil.Lock(filepath.Join(paths.StateDir(), "state.lock"))
	if err != nil {
		return err
	}
	defer unlock()
	s := Load()
	if err := change(&s); err != nil {
		return err
	}
	return writeJSON(Path(), s)
}

// writeJSON writes v to path atomically, if it fits the size the readers
// accept.
func writeJSON(path string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	if len(b) >= maxBytes {
		return fmt.Errorf("%s would be larger than %d bytes", paths.Tilde(path), maxBytes)
	}
	return fsutil.WriteFile(path, append(b, '\n'), 0o600)
}
