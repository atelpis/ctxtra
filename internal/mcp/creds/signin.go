package creds

import (
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/ctxtra/ctxtra/internal/mcp/upstream"
)

// SaveSignIn stores a new sign-in of srv under its lock, as the next
// generation: the issuer, the client and the tokens. Secret values stay.
// The caller made sure the store has a key.
func (s *Store) SaveSignIn(srv upstream.Server, issuer string, c Client, t Token) (*Record, error) {
	unlock, err := s.lock(srv)
	if err != nil {
		return nil, err
	}
	defer unlock()
	r, err := s.updateLocked(srv, true, func(r *Record) error {
		r.Issuer, r.Client, r.Token = issuer, c, t
		r.Generation++
		return nil
	})
	if err != nil {
		return nil, err
	}
	_ = s.writeState(fileID(srv.ID), func(st *State) {
		signedIn(r)(st)
		st.Scopes = nil
	})
	return r, nil
}

// SetSecrets replaces srv's secret values under its lock. The caller made
// sure the store has a key.
// For a server without OAuth, its state then says it is signed in.
func (s *Store) SetSecrets(srv upstream.Server, env, headers map[string]string) error {
	unlock, err := s.lock(srv)
	if err != nil {
		return err
	}
	defer unlock()
	r, err := s.updateLocked(srv, true, func(r *Record) error {
		r.Secrets = Secrets{Env: maps.Clone(env), Headers: maps.Clone(headers)}
		return nil
	})
	if err != nil {
		return err
	}
	if r.Token.Access == "" {
		_ = s.writeState(fileID(srv.ID), signedIn(r))
	}
	return nil
}

// SetClientSecret stores the secret of srv's pre-registered client under
// its lock. The caller made sure the store has a key.
func (s *Store) SetClientSecret(srv upstream.Server, secret string) error {
	_, err := s.Update(srv, true, func(r *Record) error {
		r.Client.ID, r.Client.Secret = srv.ClientID, secret
		return nil
	})
	return err
}

// Secrets are srv's secret values: the variables of its SecretEnv for a
// local server, the headers of its SecretHeaders for a remote one. Every
// name must have a value, and the record must have been made for srv as
// it is defined now; otherwise there are none.
func (s *Store) Secrets(srv upstream.Server) (env, headers map[string]string, err error) {
	if len(srv.SecretEnv) == 0 && len(srv.SecretHeaders) == 0 {
		return nil, nil, nil
	}
	r, err := s.Load(srv)
	switch {
	case errors.Is(err, errUnreadable):
		return nil, nil, &LoginError{srv.Name, "its secret values can't be read"}
	case err != nil:
		return nil, nil, err
	case r == nil:
		return nil, nil, &LoginError{srv.Name, "its secret values are not set"}
	case !r.Matches(srv):
		return nil, nil, &LoginError{srv.Name, "its secret values were set for another address or command"}
	}
	env, headers = map[string]string{}, map[string]string{}
	var missing []string
	for _, k := range srv.SecretEnv {
		v, ok := r.Secrets.Env[k]
		if !ok {
			missing = append(missing, k)
		}
		env[k] = v
	}
	for _, k := range srv.SecretHeaders {
		v, ok := lookupHeader(r.Secrets.Headers, k)
		if !ok {
			missing = append(missing, k)
		}
		headers[k] = v
	}
	if len(missing) > 0 {
		slices.Sort(missing)
		return nil, nil, &LoginError{srv.Name, fmt.Sprintf("no value is set for %s", strings.Join(missing, ", "))}
	}
	return env, headers, nil
}

// lookupHeader finds a header's value; header names ignore case.
func lookupHeader(h map[string]string, name string) (string, bool) {
	for k, v := range h {
		if strings.EqualFold(k, name) {
			return v, true
		}
	}
	return "", false
}
