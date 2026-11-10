// Package oauthtest is an OAuth authorization server with a protected MCP
// resource, for tests. Options turn on the behaviors of real providers
// that ctxtra must handle: missing metadata, a wrong issuer, missing PKCE
// methods, rotating refresh tokens, revoked tokens and missing scopes.
package oauthtest

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// Options are the behaviors of one server. The zero value is a server
// that follows the MCP authorization spec, with dynamic registration,
// S256, refresh tokens that rotate, and tokens without an expiry.
type Options struct {
	// NoResourceMetadata leaves out the protected resource metadata, as
	// Atlassian does: 401 answers have no resource_metadata either.
	NoResourceMetadata bool
	// WrongIssuer makes the metadata's issuer differ from where it is.
	WrongIssuer bool
	// PKCEMethods are the code_challenge_methods_supported; nil is S256.
	// NoPKCE leaves the field out.
	PKCEMethods []string
	NoPKCE      bool
	// CIMD sets client_id_metadata_document_supported: an HTTPS client_id
	// is accepted without registration.
	CIMD bool
	// NoDCR leaves out the registration endpoint.
	NoDCR bool
	// Clients are pre-registered clients: ID to secret, "" for a public
	// client. They accept any loopback redirect.
	Clients map[string]string
	// Iss sets authorization_response_iss_parameter_supported, and adds
	// iss to redirects.
	Iss bool
	// Lifetime is how long access tokens are valid; 0 issues them without
	// expires_in, valid until revoked.
	Lifetime time.Duration
	// NoRefresh issues no refresh tokens. KeepRefresh answers a refresh
	// without a new refresh token, and the old one stays valid; otherwise
	// each refresh token works once.
	NoRefresh   bool
	KeepRefresh bool
	// StrictRedirect accepts only a registered redirect URI as it is;
	// otherwise a loopback redirect may use any port (RFC 8252).
	StrictRedirect bool
	// HTTPEndpoints puts http:// endpoints in the metadata.
	HTTPEndpoints bool
	// Scopes are the scopes_supported, in both metadata documents.
	Scopes []string
	// RefreshDelay is how long the token endpoint waits before it answers
	// a refresh. OnRefresh is called when a refresh request arrives,
	// before that wait.
	RefreshDelay time.Duration
	OnRefresh    func()
	// Resource serves requests with a valid token at /mcp; nil answers
	// 200 with {"ok":true}.
	Resource http.Handler
	// Account adds an ID token with this email to sign-in answers.
	Account string
}

// Server is a running fake authorization server, also its resource
// server at /mcp. Its URL is the issuer.
type Server struct {
	*httptest.Server
	o Options

	mu        sync.Mutex
	clients   map[string]*client
	codes     map[string]*code
	access    map[string]*token
	refresh   map[string]*grant // refresh token → its grant; nil once used
	required  []string
	refreshes int
}

type client struct {
	secret    string
	redirects []string
}

// grant is what a sign-in allowed; refresh tokens keep it.
type grant struct {
	client string
	scopes []string
}

type code struct {
	grant
	redirect, challenge, method, resource string
}

type token struct {
	grant
	expiry  time.Time // zero: no expiry
	revoked bool
}

// New starts a server with TLS, closed when t ends. Its Client trusts it.
func New(t testing.TB, o Options) *Server {
	s := &Server{o: o, clients: map[string]*client{}, codes: map[string]*code{}, access: map[string]*token{},
		refresh: map[string]*grant{}}
	for id, secret := range o.Clients {
		s.clients[id] = &client{secret: secret}
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /.well-known/oauth-protected-resource", s.resourceMetadata)
	mux.HandleFunc("GET /.well-known/oauth-protected-resource/{path...}", s.resourceMetadata)
	mux.HandleFunc("GET /.well-known/oauth-authorization-server", s.serverMetadata)
	mux.HandleFunc("POST /register", s.register)
	mux.HandleFunc("GET /authorize", s.authorize)
	mux.HandleFunc("POST /token", s.token)
	mux.Handle("/mcp", s.Protect(o.Resource))
	s.Server = httptest.NewTLSServer(mux)
	t.Cleanup(s.Close)
	return s
}

// ResourceURL is the protected MCP endpoint.
func (s *Server) ResourceURL() string { return s.URL + "/mcp" }

// ResourceMetadataURL is where the protected resource metadata is.
func (s *Server) ResourceMetadataURL() string {
	return s.URL + "/.well-known/oauth-protected-resource/mcp"
}

// TokenURL is the token endpoint.
func (s *Server) TokenURL() string { return s.URL + "/token" }

// Refreshes is how many refresh requests the server answered with tokens.
func (s *Server) Refreshes() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.refreshes
}

// Require makes the resource answer 403 insufficient_scope to tokens
// without these scopes.
func (s *Server) Require(scopes ...string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.required = scopes
}

// Revoke makes the resource reject the access token with 401.
func (s *Server) Revoke(access string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if t, ok := s.access[access]; ok {
		t.revoked = true
	}
}

// AddClient registers a client, as a user's own OAuth app is, with the
// redirect URIs it accepts.
func (s *Server) AddClient(id, secret string, redirects ...string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.clients[id] = &client{secret: secret, redirects: redirects}
}

// Tokens are a token endpoint's answer.
type Tokens struct {
	IDToken      string `json:"id_token,omitempty"`
	AccessToken  string `json:"access_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int    `json:"expires_in,omitempty"`
	RefreshToken string `json:"refresh_token,omitempty"`
	Scope        string `json:"scope,omitempty"`
}

// SignIn issues tokens for a client without the browser steps, as a
// finished sign-in would.
func (s *Server) SignIn(clientID string, scopes ...string) Tokens {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.issueLocked(grant{client: clientID, scopes: scopes}, true)
}

func (s *Server) issueLocked(g grant, withRefresh bool) Tokens {
	out := Tokens{AccessToken: random("at"), TokenType: "Bearer", Scope: strings.Join(g.scopes, " ")}
	t := &token{grant: g}
	if s.o.Lifetime > 0 {
		t.expiry = time.Now().Add(s.o.Lifetime)
		out.ExpiresIn = int(s.o.Lifetime / time.Second)
	}
	s.access[out.AccessToken] = t
	if withRefresh && !s.o.NoRefresh {
		out.RefreshToken = random("rt")
		gc := g
		s.refresh[out.RefreshToken] = &gc
	}
	return out
}

func (s *Server) issuer() string {
	if s.o.WrongIssuer {
		return s.URL + "/other"
	}
	return s.URL
}

func (s *Server) endpoint(path string) string {
	u := s.URL + path
	if s.o.HTTPEndpoints {
		u = "http://" + strings.TrimPrefix(u, "https://")
	}
	return u
}

func (s *Server) resourceMetadata(w http.ResponseWriter, r *http.Request) {
	if s.o.NoResourceMetadata {
		http.NotFound(w, r)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"resource":              s.ResourceURL(),
		"authorization_servers": []string{s.URL},
		"scopes_supported":      s.o.Scopes,
	})
}

func (s *Server) serverMetadata(w http.ResponseWriter, _ *http.Request) {
	m := map[string]any{
		"issuer":                                s.issuer(),
		"authorization_endpoint":                s.endpoint("/authorize"),
		"token_endpoint":                        s.endpoint("/token"),
		"response_types_supported":              []string{"code"},
		"grant_types_supported":                 []string{"authorization_code", "refresh_token"},
		"token_endpoint_auth_methods_supported": []string{"none", "client_secret_basic", "client_secret_post"},
	}
	if !s.o.NoDCR {
		m["registration_endpoint"] = s.endpoint("/register")
	}
	if !s.o.NoPKCE {
		methods := s.o.PKCEMethods
		if methods == nil {
			methods = []string{"S256"}
		}
		m["code_challenge_methods_supported"] = methods
	}
	if s.o.CIMD {
		m["client_id_metadata_document_supported"] = true
	}
	if s.o.Iss {
		m["authorization_response_iss_parameter_supported"] = true
	}
	if s.o.Scopes != nil {
		m["scopes_supported"] = s.o.Scopes
	}
	writeJSON(w, http.StatusOK, m)
}

func (s *Server) register(w http.ResponseWriter, r *http.Request) {
	if s.o.NoDCR {
		http.NotFound(w, r)
		return
	}
	var req struct {
		RedirectURIs []string `json:"redirect_uris"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || len(req.RedirectURIs) == 0 {
		oauthError(w, http.StatusBadRequest, "invalid_client_metadata")
		return
	}
	id := random("client")
	s.AddClient(id, "", req.RedirectURIs...)
	writeJSON(w, http.StatusCreated, map[string]any{
		"client_id":                  id,
		"redirect_uris":              req.RedirectURIs,
		"token_endpoint_auth_method": "none",
		"grant_types":                []string{"authorization_code", "refresh_token"},
	})
}

// redirectOK reports whether c accepts redirect. Errors before the
// redirect is known good are shown, not redirected (RFC 6749 4.1.2.1).
func (s *Server) redirectOK(id string, c *client, redirect string) bool {
	if c == nil {
		// A client ID metadata document names the client.
		return s.o.CIMD && strings.HasPrefix(id, "https://") && loopback(redirect)
	}
	if slices.Contains(c.redirects, redirect) {
		return true
	}
	if s.o.StrictRedirect || !loopback(redirect) {
		return false
	}
	if len(c.redirects) == 0 {
		return true
	}
	// Any port for a registered loopback redirect.
	got, _ := url.Parse(redirect)
	for _, r := range c.redirects {
		if u, err := url.Parse(r); err == nil && u.Hostname() == got.Hostname() && u.Path == got.Path {
			return true
		}
	}
	return false
}

func loopback(redirect string) bool {
	u, err := url.Parse(redirect)
	return err == nil && u.Scheme == "http" && (u.Hostname() == "127.0.0.1" || u.Hostname() == "::1")
}

func (s *Server) authorize(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	id, redirect := q.Get("client_id"), q.Get("redirect_uri")
	s.mu.Lock()
	c := s.clients[id]
	ok := s.redirectOK(id, c, redirect)
	s.mu.Unlock()
	if !ok {
		http.Error(w, "unknown client or redirect URI", http.StatusBadRequest)
		return
	}
	back := func(params url.Values) {
		params.Set("state", q.Get("state"))
		if s.o.Iss {
			params.Set("iss", s.URL)
		}
		u, _ := url.Parse(redirect)
		u.RawQuery = params.Encode()
		http.Redirect(w, r, u.String(), http.StatusFound)
	}
	fail := func(code string) { back(url.Values{"error": {code}}) }
	method := q.Get("code_challenge_method")
	switch {
	case q.Get("response_type") != "code":
		fail("unsupported_response_type")
	case q.Get("code_challenge") == "" || !slices.Contains(s.pkceMethods(), method):
		fail("invalid_request")
	case q.Get("resource") != s.ResourceURL():
		fail("invalid_target")
	default:
		c := &code{grant: grant{client: id, scopes: strings.Fields(q.Get("scope"))}, redirect: redirect,
			challenge: q.Get("code_challenge"), method: method, resource: q.Get("resource")}
		v := random("code")
		s.mu.Lock()
		s.codes[v] = c
		s.mu.Unlock()
		back(url.Values{"code": {v}})
	}
}

func (s *Server) pkceMethods() []string {
	switch {
	case s.o.NoPKCE:
		return nil
	case s.o.PKCEMethods == nil:
		return []string{"S256"}
	}
	return s.o.PKCEMethods
}

// clientAuth is the client that authenticated the token request, "" when
// it failed.
func (s *Server) clientAuth(r *http.Request) string {
	id, secret, basic := r.BasicAuth()
	if !basic {
		id, secret = r.PostForm.Get("client_id"), r.PostForm.Get("client_secret")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if c, ok := s.clients[id]; ok && c.secret == secret {
		return id
	}
	if c := s.clients[id]; c == nil && s.o.CIMD && strings.HasPrefix(id, "https://") && secret == "" {
		return id
	}
	return ""
}

func (s *Server) token(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		oauthError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	id := s.clientAuth(r)
	if id == "" {
		oauthError(w, http.StatusUnauthorized, "invalid_client")
		return
	}
	f := r.PostForm
	if f.Get("resource") != s.ResourceURL() {
		oauthError(w, http.StatusBadRequest, "invalid_target")
		return
	}
	switch f.Get("grant_type") {
	case "authorization_code":
		s.mu.Lock()
		c := s.codes[f.Get("code")]
		delete(s.codes, f.Get("code"))
		s.mu.Unlock()
		if c == nil || c.client != id || c.redirect != f.Get("redirect_uri") || !verifier(c.method, c.challenge, f.Get("code_verifier")) {
			oauthError(w, http.StatusBadRequest, "invalid_grant")
			return
		}
		s.mu.Lock()
		out := s.issueLocked(c.grant, true)
		s.mu.Unlock()
		if s.o.Account != "" {
			claims, _ := json.Marshal(map[string]string{"email": s.o.Account})
			out.IDToken = "e30." + base64.RawURLEncoding.EncodeToString(claims) + ".sig"
		}
		writeJSON(w, http.StatusOK, out)
	case "refresh_token":
		if s.o.OnRefresh != nil {
			s.o.OnRefresh()
		}
		time.Sleep(s.o.RefreshDelay)
		s.mu.Lock()
		defer s.mu.Unlock()
		rt := f.Get("refresh_token")
		g := s.refresh[rt]
		if g == nil || g.client != id {
			oauthError(w, http.StatusBadRequest, "invalid_grant")
			return
		}
		s.refreshes++
		if s.o.KeepRefresh {
			writeJSON(w, http.StatusOK, s.issueLocked(*g, false))
			return
		}
		s.refresh[rt] = nil
		writeJSON(w, http.StatusOK, s.issueLocked(*g, true))
	default:
		oauthError(w, http.StatusBadRequest, "unsupported_grant_type")
	}
}

func verifier(method, challenge, v string) bool {
	if v == "" {
		return false
	}
	if method == "plain" {
		return v == challenge
	}
	sum := sha256.Sum256([]byte(v))
	return base64.RawURLEncoding.EncodeToString(sum[:]) == challenge
}

// Protect serves next to requests with a valid bearer token: 401 for a
// missing, unknown, expired or revoked token, 403 insufficient_scope for
// a token without the required scopes. A nil next answers {"ok":true}.
func (s *Server) Protect(next http.Handler) http.Handler {
	if next == nil {
		next = http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
		})
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		v, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		s.mu.Lock()
		t := s.access[v]
		required := s.required
		s.mu.Unlock()
		if !ok || t == nil || t.revoked || !t.expiry.IsZero() && time.Now().After(t.expiry) {
			challenge := `Bearer error="invalid_token"`
			if !s.o.NoResourceMetadata {
				challenge += `, resource_metadata="` + s.ResourceMetadataURL() + `"`
			}
			w.Header().Set("WWW-Authenticate", challenge)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		for _, sc := range required {
			if !slices.Contains(t.scopes, sc) {
				w.Header().Set("WWW-Authenticate", `Bearer error="insufficient_scope", scope="`+strings.Join(required, " ")+`"`)
				w.WriteHeader(http.StatusForbidden)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func oauthError(w http.ResponseWriter, status int, code string) {
	writeJSON(w, status, map[string]string{"error": code})
}

var counter struct {
	sync.Mutex
	n int
}

// random is a unique, unguessable value with a readable prefix.
func random(prefix string) string {
	b := make([]byte, 12)
	_, _ = rand.Read(b)
	counter.Lock()
	counter.n++
	n := counter.n
	counter.Unlock()
	return prefix + "-" + strconv.Itoa(n) + "-" + base64.RawURLEncoding.EncodeToString(b)
}
