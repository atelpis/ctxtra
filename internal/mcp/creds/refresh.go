package creds

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/ctxtra/ctxtra/internal/fsutil"
	"github.com/ctxtra/ctxtra/internal/mcp/upstream"
)

const (
	// refreshMargin is how long before its expiry a token is refreshed;
	// half its lifetime when that is shorter.
	refreshMargin = 5 * time.Minute
	// refreshTimeout bounds a refresh request. It is shorter than the
	// lock wait, so a session waiting for another's refresh gets the
	// result rather than a timeout.
	refreshTimeout = 20 * time.Second
)

// LoginError is a server whose credentials only ctxtra mcp login can
// restore: it was never signed in, its refresh token was rejected, or the
// stored sign-in is for another definition.
type LoginError struct {
	Name   string
	Reason string
}

func (e *LoginError) Error() string {
	return e.Name + ": " + e.Reason + "; run ctxtra mcp login " + e.Name
}

// CanonicalURL is u as credentials are bound to it: scheme and host in
// lower case, without a default port, a fragment or user information.
func CanonicalURL(u string) string {
	p, err := url.Parse(u)
	if err != nil || p.Host == "" {
		return u
	}
	p.Scheme = strings.ToLower(p.Scheme)
	host := strings.ToLower(p.Hostname())
	port := p.Port()
	if p.Scheme == "https" && port == "443" || p.Scheme == "http" && port == "80" {
		port = ""
	}
	p.Host = host
	if port != "" {
		p.Host = net.JoinHostPort(host, port)
	} else if strings.Contains(host, ":") {
		p.Host = "[" + host + "]"
	}
	p.User, p.Fragment, p.RawFragment = nil, "", ""
	return p.String()
}

// fresh reports whether r's access token can be used without a refresh.
func (s *Store) fresh(r *Record) bool {
	if r.Token.Access == "" {
		return false
	}
	if r.Token.Expiry.IsZero() {
		return true
	}
	margin := refreshMargin
	if !r.Token.Obtained.IsZero() {
		margin = min(margin, r.Token.Expiry.Sub(r.Token.Obtained)/2)
	}
	return r.Token.Expiry.Sub(s.now()) > margin
}

// usable checks a record read for srv before its tokens are used.
func usable(srv upstream.Server, r *Record, err error) error {
	switch {
	case errors.Is(err, errUnreadable):
		return &LoginError{srv.Name, "its credentials can't be read"}
	case err != nil:
		return err
	case r == nil || r.Token.Access == "":
		return &LoginError{srv.Name, "not signed in"}
	case !r.Matches(srv):
		return &LoginError{srv.Name, "the sign-in was for another address"}
	}
	return nil
}

// Token is the access token for srv. When the stored one expires soon, it
// is refreshed under srv's lock, and the new tokens are saved before they
// are returned. A token without an expiry is used until the server
// rejects it.
func (s *Store) Token(ctx context.Context, srv upstream.Server) (string, error) {
	r, err := s.loadShared(srv)
	if err := usable(srv, r, err); err != nil {
		return "", err
	}
	if s.fresh(r) {
		return r.Token.Access, nil
	}
	return s.refreshLocked(ctx, srv, false, func(cur *Record) bool { return s.fresh(cur) })
}

// loadShared reads srv's record under its lock taken shared: no refresh
// holds it then, so the record read is on the disk, synced, and its
// tokens can be used.
func (s *Store) loadShared(srv upstream.Server) (*Record, error) {
	if err := s.mkdir("locks"); err != nil {
		return nil, err
	}
	unlock, err := fsutil.LockShared(s.lockPath(fileID(srv.ID)), s.lockTimeout())
	if err != nil {
		return nil, s.lockError("the credentials of "+srv.Name, err)
	}
	defer unlock()
	return s.Load(srv)
}

// Rejected is Token after srv rejected the access token with 401: it
// refreshes once under the lock, unless another process stored a new
// token since, and returns the token to retry with.
func (s *Store) Rejected(ctx context.Context, srv upstream.Server, rejected string) (string, error) {
	return s.refreshLocked(ctx, srv, true, func(cur *Record) bool { return cur.Token.Access != rejected && s.fresh(cur) })
}

// refreshLocked takes srv's lock, reads the record again, and returns its
// token when done says another process already refreshed it; otherwise it
// refreshes and saves. rejected says the server rejected the stored token.
func (s *Store) refreshLocked(ctx context.Context, srv upstream.Server, rejected bool, done func(*Record) bool) (string, error) {
	unlock, err := s.lock(srv)
	if err != nil {
		return "", err
	}
	defer unlock()
	if err := s.checkVersion(srv.ID, srv.Name); err != nil {
		return "", err
	}
	r, err := s.load(srv.ID)
	if err := usable(srv, r, err); err != nil {
		return "", err
	}
	if done(r) {
		return r.Token.Access, nil
	}
	if r.Token.Refresh == "" {
		// Without a refresh token, the token is used until it expires.
		if !rejected && (r.Token.Expiry.IsZero() || s.now().Before(r.Token.Expiry)) {
			return r.Token.Access, nil
		}
		reason := "the sign-in expired"
		if r.Token.Expiry.IsZero() {
			reason = "the server rejected the sign-in"
		}
		return "", s.needsLogin(srv, reason)
	}
	tok, err := s.refresh(ctx, r)
	if errors.Is(err, errInvalidGrant) {
		return "", s.needsLogin(srv, "the sign-in expired or was revoked")
	}
	if err != nil {
		return "", fmt.Errorf("%s: refreshing the sign-in failed: %w", srv.Name, err)
	}
	if tok.Refresh == "" {
		tok.Refresh = r.Token.Refresh
	}
	if len(tok.Scopes) == 0 {
		tok.Scopes = r.Token.Scopes
	}
	r.Token = tok
	if err := s.save(r); err != nil {
		return "", fmt.Errorf("the credentials of %s could not be saved: %w", srv.Name, err)
	}
	_ = s.writeState(fileID(srv.ID), signedIn(r))
	return tok.Access, nil
}

// needsLogin records that srv needs sign-in, and is the error to return.
// The caller holds the lock.
func (s *Store) needsLogin(srv upstream.Server, reason string) error {
	_ = s.writeState(fileID(srv.ID), func(st *State) {
		st.SignedIn, st.NeedsLogin, st.Reason = false, true, reason
	})
	return &LoginError{srv.Name, reason}
}

// oauthErrors are the error codes of a token endpoint (RFC 6749 5.2,
// RFC 8707).
var oauthErrors = []string{"invalid_request", "invalid_client", "unauthorized_client", "unsupported_grant_type",
	"invalid_scope", "invalid_target", "temporarily_unavailable", "server_error"}

// errInvalidGrant is a refresh token that the authorization server
// rejected.
var errInvalidGrant = errors.New("invalid_grant")

// tokenResponse is a token endpoint's answer (RFC 6749 5.1, 5.2).
type tokenResponse struct {
	AccessToken  string      `json:"access_token"`
	TokenType    string      `json:"token_type"`
	ExpiresIn    json.Number `json:"expires_in"`
	RefreshToken string      `json:"refresh_token"`
	Scope        string      `json:"scope"`
	Error        string      `json:"error"`
}

// refresh asks r's token endpoint for new tokens. Errors name the OAuth
// error code only: a provider's description can repeat request values.
func (s *Store) refresh(ctx context.Context, r *Record) (Token, error) {
	if err := checkEndpoint(r.Client.TokenURL, r.URL); err != nil {
		return Token{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, refreshTimeout)
	defer cancel()
	form := url.Values{"grant_type": {"refresh_token"}, "refresh_token": {r.Token.Refresh}}
	if r.URL != "" {
		form.Set("resource", r.URL)
	}
	basic := r.Client.AuthMethod == "client_secret_basic" && r.Client.Secret != ""
	if !basic {
		form.Set("client_id", r.Client.ID)
		if r.Client.Secret != "" {
			form.Set("client_secret", r.Client.Secret)
		}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, r.Client.TokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return Token{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	if basic {
		req.SetBasicAuth(url.QueryEscape(r.Client.ID), url.QueryEscape(r.Client.Secret))
	}
	client := http.DefaultClient
	if s.HTTP != nil {
		client = s.HTTP
	}
	// A redirect would send the refresh token and client secret on to
	// another address: never follow one.
	noRedirect := *client
	noRedirect.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	client = &noRedirect
	now := s.now()
	resp, err := client.Do(req)
	if err != nil {
		// Not the error's text: it can quote the URL, or a header of the
		// answer.
		if errors.Is(err, context.DeadlineExceeded) {
			return Token{}, fmt.Errorf("the token endpoint %s didn't answer in time", req.URL.Host)
		}
		return Token{}, fmt.Errorf("can't reach the token endpoint %s", req.URL.Host)
	}
	defer resp.Body.Close()
	var tr tokenResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxRecord)).Decode(&tr); err != nil && resp.StatusCode == http.StatusOK {
		return Token{}, errors.New("the token endpoint's answer isn't JSON")
	}
	if resp.StatusCode != http.StatusOK || tr.Error != "" {
		if tr.Error == "invalid_grant" {
			return Token{}, errInvalidGrant
		}
		// Only known codes: the text is the provider's, and can repeat
		// what was sent.
		if slices.Contains(oauthErrors, tr.Error) {
			return Token{}, fmt.Errorf("the token endpoint answered %s", tr.Error)
		}
		return Token{}, fmt.Errorf("the token endpoint answered HTTP %d", resp.StatusCode)
	}
	if tr.AccessToken == "" {
		return Token{}, errors.New("the token endpoint's answer has no access token")
	}
	return newToken(tr, now), nil
}

// newToken is the Token of a token endpoint's answer, received at now.
func newToken(tr tokenResponse, now time.Time) Token {
	t := Token{Access: tr.AccessToken, Refresh: tr.RefreshToken, Type: tr.TokenType, Obtained: now.UTC(),
		Scopes: strings.Fields(tr.Scope)}
	if n, err := tr.ExpiresIn.Int64(); err == nil && n > 0 {
		t.Expiry = now.Add(time.Duration(n) * time.Second).UTC()
	}
	return t
}

// checkEndpoint refuses an endpoint that would send credentials in the
// clear: HTTPS only, except loopback endpoints of a loopback server.
func checkEndpoint(endpoint, server string) error {
	u, err := url.Parse(endpoint)
	if err != nil || u.Host == "" {
		return errors.New("the stored token endpoint is not a URL")
	}
	if u.Scheme == "https" {
		return nil
	}
	if su, err := url.Parse(server); err == nil && u.Scheme == "http" && isLoopback(u.Hostname()) && isLoopback(su.Hostname()) {
		return nil
	}
	return fmt.Errorf("the token endpoint %s doesn't use HTTPS", u.Scheme+"://"+u.Host)
}

func isLoopback(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
