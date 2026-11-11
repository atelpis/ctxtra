package creds

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/ctxtra/ctxtra/internal/fsutil"
	"github.com/ctxtra/ctxtra/internal/mcp/upstream"
)

// State is a server's state file: what the hook, mcp list and mcp status
// show without the key. It holds no secret.
type State struct {
	Version int `json:"version"`
	// Name is the server's name, Issuer the authorization server of its
	// sign-in.
	Name     string `json:"name"`
	Issuer   string `json:"issuer"`
	SignedIn bool   `json:"signed_in"`
	// NeedsLogin is set when only ctxtra mcp login can make the server
	// work again; Reason says why.
	NeedsLogin bool   `json:"needs_login"`
	Reason     string `json:"reason"`
	// Scopes are those that a server asked for with 403
	// insufficient_scope, for the next login.
	Scopes     []string  `json:"scopes"`
	Generation int64     `json:"generation"`
	Updated    time.Time `json:"updated"`

	raw []byte
}

// State reads srv's state file without the lock: the zero State when
// there is none, and a NewerError for one that a newer ctxtra wrote.
func (s *Store) State(srv upstream.Server) (State, error) {
	st, err := s.readState(fileID(srv.ID))
	if err == nil && st.Version > stateVersion {
		return State{}, &NewerError{srv.Name}
	}
	return st, err
}

func (s *Store) readState(fid string) (State, error) {
	data, err := fsutil.ReadBounded(s.statePath(fid), maxRecord)
	if errors.Is(err, os.ErrNotExist) {
		return State{}, nil
	}
	if err != nil {
		return State{}, err
	}
	st := State{raw: data}
	if err := json.Unmarshal(data, &st); err != nil {
		return State{}, err
	}
	return st, nil
}

// writeState changes the state file of fid. The caller holds its lock. A
// state file of a newer version is left as it is.
func (s *Store) writeState(fid string, change func(*State)) error {
	st, err := s.readState(fid)
	if err != nil {
		// A damaged state file is rewritten.
		st = State{}
	}
	if st.Version > stateVersion {
		return nil
	}
	change(&st)
	st.Version, st.Updated = stateVersion, s.now().UTC()
	data, err := json.Marshal(st)
	if err != nil {
		return err
	}
	if data, err = mergeUnknown(st.raw, data, nil); err != nil {
		return err
	}
	return writeDurable(s.statePath(fid), append(data, '\n'))
}

// signedIn is the state after a working sign-in or refresh of r.
func signedIn(r *Record) func(*State) {
	return func(st *State) {
		st.SignedIn, st.NeedsLogin, st.Reason, st.Generation, st.Issuer = true, false, "", r.Generation, r.Issuer
	}
}

// ServersOf are the names of the servers but except that are signed in
// with issuer, from their state files: a sign-in to one more of them may
// get the browser's active account.
func (s *Store) ServersOf(issuer string, except upstream.Server) []string {
	if issuer == "" {
		return nil
	}
	files, _ := filepath.Glob(filepath.Join(s.Dir, "state", "*.json"))
	var out []string
	for _, f := range files {
		fid := strings.TrimSuffix(filepath.Base(f), ".json")
		if fid == fileID(except.ID) {
			continue
		}
		st, err := s.readState(fid)
		if err == nil && st.SignedIn && st.Issuer == issuer && st.Name != "" {
			out = append(out, st.Name)
		}
	}
	slices.Sort(out)
	return out
}

// setState changes srv's state file, with its name. The caller holds its
// lock.
func (s *Store) setState(srv upstream.Server, change func(*State)) error {
	return s.writeState(fileID(srv.ID), func(st *State) {
		st.Name = srv.Name
		change(st)
	})
}
