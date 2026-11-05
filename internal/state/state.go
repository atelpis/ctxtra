// Package state keeps what ctxtra learns on this machine.
//
// state.json holds what ctxtra learned about new commits of each pinned
// source and about new ctxtra releases, and the commit the user didn't
// accept. None of it changes what a session gets: losing it loses the
// notices.
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
	Check
	Head string `json:"head,omitempty"` // the branch's commit at Checked
}

// Check is the result of the update checks of one thing: a source or ctxtra
// itself.
type Check struct {
	Attempt time.Time `json:"checkAttempt,omitzero"` // the last time a check was started
	Checked time.Time `json:"checked,omitzero"`      // the last check that worked
	Error   string    `json:"checkError,omitempty"`  // why the last check failed; "" after one that worked
}

// Release is what the checks learned about ctxtra's releases.
type Release struct {
	Check
	Latest string `json:"latest,omitempty"` // the latest release's version at Checked
}

// State is the whole file.
type State struct {
	Sources map[string]Source `json:"sources"`
	Ctxtra  Release           `json:"ctxtra,omitzero"`
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

// UpdateRelease changes what the state knows about ctxtra's releases.
func UpdateRelease(change func(*Release)) error {
	return UpdateReleaseWithin(0, change)
}

// UpdateReleaseWithin is UpdateRelease that waits at most wait for the lock,
// or without a limit when wait is 0.
func UpdateReleaseWithin(wait time.Duration, change func(*Release)) error {
	return updateWithin(wait, func(s *State) error {
		change(&s.Ctxtra)
		return nil
	})
}

// ChangeWithin changes the whole state in one write, and waits at most wait
// for the lock.
func ChangeWithin(wait time.Duration, change func(*State)) error {
	return updateWithin(wait, func(s *State) error {
		change(s)
		return nil
	})
}

// Put sets the entry of the source id that follows e.Branch.
func (s State) Put(id string, e Source) { s.Sources[key(id, e.Branch)] = e }

// UpdateWithin is Update that waits at most wait for the lock, or without a
// limit when wait is 0.
func UpdateWithin(wait time.Duration, id, url, branch string, change func(*Source)) error {
	return updateWithin(wait, func(s *State) error {
		e := s.Get(id, url, branch)
		change(&e)
		s.Sources[key(id, branch)] = e
		return nil
	})
}

// Claim is a source to claim a check of, by the arguments of Get. Name is
// the caller's, returned with the claims.
type Claim struct{ Name, ID, URL, Branch string }

// ClaimChecks records a check attempt for ctxtra (when release) and for each
// source whose last attempt is at least interval old, and returns what it
// claimed, and the time it recorded. Two sessions that start at once don't both claim a check: the lock
// orders them, and the time is read under it, so the second one finds the
// first one's attempt. An attempt more than interval in the future, after the
// clock went back, is due too. It waits at most wait for the lock.
func ClaimChecks(release bool, srcs []Claim, now func() time.Time, interval, wait time.Duration) (bool, []Claim, time.Time, error) {
	unlock, err := fsutil.LockTimeout(lockPath(), wait)
	if err != nil {
		return false, nil, time.Time{}, err
	}
	defer unlock()
	s := Load()
	at := now()
	due := func(c Check) bool {
		d := at.Sub(c.Attempt)
		return d >= interval || d < -interval
	}
	claimedRelease := release && due(s.Ctxtra.Check)
	if claimedRelease {
		s.Ctxtra.Attempt = at
	}
	var claimed []Claim
	for _, c := range srcs {
		e := s.Get(c.ID, c.URL, c.Branch)
		if due(e.Check) {
			e.Attempt = at
			s.Sources[key(c.ID, c.Branch)] = e
			claimed = append(claimed, c)
		}
	}
	if !claimedRelease && len(claimed) == 0 {
		return false, nil, at, nil
	}
	return claimedRelease, claimed, at, writeJSON(Path(), s)
}

func lockPath() string { return filepath.Join(paths.StateDir(), "state.lock") }

func update(change func(*State) error) error { return updateWithin(0, change) }

func updateWithin(wait time.Duration, change func(*State) error) error {
	lock := fsutil.Lock
	if wait > 0 {
		lock = func(path string) (func(), error) { return fsutil.LockTimeout(path, wait) }
	}
	unlock, err := lock(lockPath())
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
