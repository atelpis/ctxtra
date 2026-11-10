package creds

import (
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/ctxtra/ctxtra/internal/mcp/upstream"
)

// ErrSignInChanged is a sign-in result that came too late: another
// sign-in or a removal of the server finished while it ran.
var ErrSignInChanged = errors.New("another sign-in or a removal of the server finished meanwhile; nothing was saved")

// Generation is the sign-in generation of srv's credentials, 0 without
// any. Generations come from the clock, so a server removed and signed in
// again never gets a generation that an earlier sign-in saw. A sign-in reads it before it starts and gives it to SaveSignIn.
func (s *Store) Generation(srv upstream.Server) (int64, error) {
	r, err := s.Load(srv)
	switch {
	case errors.Is(err, errUnreadable):
		return 0, nil
	case err != nil:
		return 0, err
	case r == nil:
		return 0, nil
	}
	return r.Generation, nil
}

// SaveSignIn stores a new sign-in of srv under its lock, as the next
// generation: the issuer, the client and the tokens. Secret values stay.
// It saves nothing and returns ErrSignInChanged when the credentials are
// no longer at generation gen, which the sign-in started from. The caller
// made sure the store has a key.
func (s *Store) SaveSignIn(srv upstream.Server, gen int64, issuer string, c Client, t Token) (*Record, error) {
	unlock, err := s.lock(srv)
	if err != nil {
		return nil, err
	}
	defer unlock()
	if err := s.checkVersion(srv.ID, srv.Name); err != nil {
		return nil, err
	}
	cur, err := s.load(srv.ID)
	switch {
	case errors.Is(err, errUnreadable):
	case err != nil:
		return nil, err
	case cur == nil && gen != 0, cur != nil && cur.Generation != gen:
		return nil, ErrSignInChanged
	}
	r, err := s.updateLocked(srv, true, func(r *Record) error {
		r.Issuer, r.Client, r.Token = issuer, c, t
		r.Generation = s.nextGeneration(r.Generation)
		return nil
	})
	if err != nil {
		return nil, err
	}
	_ = s.setState(srv, func(st *State) {
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
		_ = s.setState(srv, signedIn(r))
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
	r, err := s.loadShared(srv)
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

// nextGeneration is the generation of a sign-in after old: the time in
// nanoseconds, or old+1 when the clock is behind.
func (s *Store) nextGeneration(old int64) int64 {
	return max(s.now().UnixNano(), old+1)
}
