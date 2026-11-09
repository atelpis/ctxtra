package creds

import (
	"encoding/json"
	"errors"
	"os"
	"time"

	"github.com/ctxtra/ctxtra/internal/fsutil"
	"github.com/ctxtra/ctxtra/internal/mcp/upstream"
)

// State is a server's state file: what the hook, mcp list and mcp status
// show without the key. It holds no secret.
type State struct {
	Version  int  `json:"version"`
	SignedIn bool `json:"signed_in"`
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
		st.SignedIn, st.NeedsLogin, st.Reason, st.Generation = true, false, "", r.Generation
	}
}
