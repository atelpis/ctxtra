package oauthtest

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

const redirect = "http://127.0.0.1:43123/callback"

func get(t *testing.T, s *Server, u string) (*http.Response, map[string]any) {
	t.Helper()
	resp, err := s.Client().Get(u)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var m map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&m)
	return resp, m
}

func post(t *testing.T, s *Server, path string, form url.Values, user, pass string) (int, map[string]any) {
	t.Helper()
	req, _ := http.NewRequest("POST", s.URL+path, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if user != "" {
		req.SetBasicAuth(user, pass)
	}
	resp, err := s.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var m map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&m)
	return resp.StatusCode, m
}

func register(t *testing.T, s *Server) string {
	t.Helper()
	resp, err := s.Client().Post(s.URL+"/register", "application/json", strings.NewReader(`{"redirect_uris":["`+redirect+`"]}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var m map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&m)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("register: %d %v", resp.StatusCode, m)
	}
	return m["client_id"].(string)
}

// authorize runs the authorization request and returns the redirect's
// query.
func authorize(t *testing.T, s *Server, params url.Values) url.Values {
	t.Helper()
	c := *s.Client()
	c.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := c.Get(s.URL + "/authorize?" + params.Encode())
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("authorize: %d", resp.StatusCode)
	}
	u, _ := url.Parse(resp.Header.Get("Location"))
	return u.Query()
}

func challenge(v string) string {
	sum := sha256.Sum256([]byte(v))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

func authParams(s *Server, client string) url.Values {
	return url.Values{"response_type": {"code"}, "client_id": {client}, "redirect_uri": {redirect},
		"state": {"st"}, "code_challenge": {challenge("verifier")}, "code_challenge_method": {"S256"},
		"resource": {s.ResourceURL()}, "scope": {"read write"}}
}

func call(t *testing.T, s *Server, access string) *http.Response {
	t.Helper()
	req, _ := http.NewRequest("POST", s.ResourceURL(), strings.NewReader("{}"))
	req.Header.Set("Authorization", "Bearer "+access)
	resp, err := s.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	return resp
}

func TestFullSignIn(t *testing.T) {
	s := New(t, Options{Iss: true, Lifetime: time.Hour, Scopes: []string{"read", "write"}})
	resp, prm := get(t, s, s.ResourceMetadataURL())
	if resp.StatusCode != 200 || prm["resource"] != s.ResourceURL() {
		t.Fatalf("resource metadata: %d %v", resp.StatusCode, prm)
	}
	_, meta := get(t, s, s.URL+"/.well-known/oauth-authorization-server")
	if meta["issuer"] != s.URL || meta["authorization_response_iss_parameter_supported"] != true {
		t.Fatalf("metadata: %v", meta)
	}
	id := register(t, s)
	q := authorize(t, s, authParams(s, id))
	if q.Get("state") != "st" || q.Get("iss") != s.URL || q.Get("code") == "" {
		t.Fatalf("redirect: %v", q)
	}
	form := url.Values{"grant_type": {"authorization_code"}, "code": {q.Get("code")}, "redirect_uri": {redirect},
		"code_verifier": {"verifier"}, "client_id": {id}, "resource": {s.ResourceURL()}}
	status, tok := post(t, s, "/token", form, "", "")
	if status != 200 || tok["refresh_token"] == nil || tok["expires_in"] == nil {
		t.Fatalf("token: %d %v", status, tok)
	}
	// A code works once.
	if status, _ := post(t, s, "/token", form, "", ""); status != 400 {
		t.Errorf("reused code: %d", status)
	}
	if resp := call(t, s, tok["access_token"].(string)); resp.StatusCode != 200 {
		t.Errorf("call: %d", resp.StatusCode)
	}
}

func TestPKCE(t *testing.T) {
	s := New(t, Options{})
	id := register(t, s)
	p := authParams(s, id)
	p.Set("code_challenge_method", "plain")
	if q := authorize(t, s, p); q.Get("error") != "invalid_request" {
		t.Errorf("plain without listing it: %v", q)
	}
	q := authorize(t, s, authParams(s, id))
	form := url.Values{"grant_type": {"authorization_code"}, "code": {q.Get("code")}, "redirect_uri": {redirect},
		"code_verifier": {"wrong"}, "client_id": {id}, "resource": {s.ResourceURL()}}
	if status, _ := post(t, s, "/token", form, "", ""); status != 400 {
		t.Errorf("wrong verifier: %d", status)
	}

	none := New(t, Options{NoPKCE: true})
	if _, m := get(t, none, none.URL+"/.well-known/oauth-authorization-server"); m["code_challenge_methods_supported"] != nil {
		t.Errorf("NoPKCE lists methods: %v", m)
	}
	both := New(t, Options{PKCEMethods: []string{"plain", "S256"}})
	if _, m := get(t, both, both.URL+"/.well-known/oauth-authorization-server"); len(m["code_challenge_methods_supported"].([]any)) != 2 {
		t.Errorf("methods: %v", m)
	}
}

func TestResourceAndRedirect(t *testing.T) {
	s := New(t, Options{StrictRedirect: true})
	id := register(t, s)
	p := authParams(s, id)
	p.Set("resource", s.URL+"/other")
	if q := authorize(t, s, p); q.Get("error") != "invalid_target" {
		t.Errorf("wrong resource: %v", q)
	}
	p = authParams(s, id)
	p.Set("redirect_uri", "http://127.0.0.1:1/callback")
	c := *s.Client()
	c.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := c.Get(s.URL + "/authorize?" + p.Encode())
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("another port with StrictRedirect: %d", resp.StatusCode)
	}
	loose := New(t, Options{})
	id = register(t, loose)
	p = authParams(loose, id)
	p.Set("redirect_uri", "http://127.0.0.1:1/callback")
	if q := authorize(t, loose, p); q.Get("code") == "" {
		t.Errorf("another loopback port: %v", q)
	}
}

func TestMetadataVariants(t *testing.T) {
	s := New(t, Options{NoResourceMetadata: true, WrongIssuer: true, NoDCR: true, CIMD: true, HTTPEndpoints: true})
	if resp, _ := get(t, s, s.ResourceMetadataURL()); resp.StatusCode != 404 {
		t.Errorf("resource metadata: %d", resp.StatusCode)
	}
	_, m := get(t, s, s.URL+"/.well-known/oauth-authorization-server")
	if m["issuer"] == s.URL || m["registration_endpoint"] != nil || m["client_id_metadata_document_supported"] != true ||
		!strings.HasPrefix(m["token_endpoint"].(string), "http://") {
		t.Errorf("metadata: %v", m)
	}
	resp := call(t, s, "nothing")
	if resp.StatusCode != 401 || strings.Contains(resp.Header.Get("WWW-Authenticate"), "resource_metadata") {
		t.Errorf("401 without metadata: %d %q", resp.StatusCode, resp.Header.Get("WWW-Authenticate"))
	}
	// A client ID metadata document URL needs no registration.
	q := authorize(t, s, authParams(s, "https://ctxtra.example/client.json"))
	if q.Get("code") == "" {
		t.Errorf("CIMD client: %v", q)
	}
}

func TestPreRegisteredClient(t *testing.T) {
	s := New(t, Options{NoDCR: true, Clients: map[string]string{"app": "s3cret"}})
	q := authorize(t, s, authParams(s, "app"))
	form := url.Values{"grant_type": {"authorization_code"}, "code": {q.Get("code")}, "redirect_uri": {redirect},
		"code_verifier": {"verifier"}, "resource": {s.ResourceURL()}}
	if status, _ := post(t, s, "/token", form, "app", "wrong"); status != 401 {
		t.Errorf("wrong secret: %d", status)
	}
	q = authorize(t, s, authParams(s, "app"))
	form.Set("code", q.Get("code"))
	if status, tok := post(t, s, "/token", form, "app", "s3cret"); status != 200 || tok["expires_in"] != nil {
		t.Errorf("token: %d %v", status, tok)
	}
}

func TestRefreshRotation(t *testing.T) {
	s := New(t, Options{Clients: map[string]string{"app": ""}})
	tok := s.SignIn("app")
	form := url.Values{"grant_type": {"refresh_token"}, "refresh_token": {tok.RefreshToken}, "client_id": {"app"},
		"resource": {s.ResourceURL()}}
	status, next := post(t, s, "/token", form, "", "")
	if status != 200 || next["refresh_token"] == tok.RefreshToken {
		t.Fatalf("refresh: %d %v", status, next)
	}
	if status, m := post(t, s, "/token", form, "", ""); status != 400 || m["error"] != "invalid_grant" {
		t.Errorf("reused refresh token: %d %v", status, m)
	}
	if s.Refreshes() != 1 {
		t.Errorf("refreshes: %d", s.Refreshes())
	}
	form.Set("resource", "https://elsewhere.example/mcp")
	form.Set("refresh_token", next["refresh_token"].(string))
	if status, _ := post(t, s, "/token", form, "", ""); status != 400 {
		t.Errorf("wrong resource: %d", status)
	}

	keep := New(t, Options{Clients: map[string]string{"app": ""}, KeepRefresh: true})
	tok = keep.SignIn("app")
	form = url.Values{"grant_type": {"refresh_token"}, "refresh_token": {tok.RefreshToken}, "client_id": {"app"},
		"resource": {keep.ResourceURL()}}
	for range 2 {
		if status, m := post(t, keep, "/token", form, "", ""); status != 200 || m["refresh_token"] != nil {
			t.Errorf("KeepRefresh: %d %v", status, m)
		}
	}
}

func TestTokensWithoutExpiryOrRefresh(t *testing.T) {
	s := New(t, Options{Clients: map[string]string{"app": ""}, NoRefresh: true})
	tok := s.SignIn("app")
	if tok.RefreshToken != "" || tok.ExpiresIn != 0 {
		t.Errorf("tokens: %+v", tok)
	}
	short := New(t, Options{Clients: map[string]string{"app": ""}, Lifetime: 50 * time.Millisecond})
	tok = short.SignIn("app")
	if resp := call(t, short, tok.AccessToken); resp.StatusCode != 200 {
		t.Errorf("fresh token: %d", resp.StatusCode)
	}
	time.Sleep(60 * time.Millisecond)
	if resp := call(t, short, tok.AccessToken); resp.StatusCode != 401 {
		t.Errorf("expired token: %d", resp.StatusCode)
	}
}

func TestRevokedAndScopes(t *testing.T) {
	s := New(t, Options{Clients: map[string]string{"app": ""}})
	tok := s.SignIn("app", "read")
	s.Require("read", "write")
	resp := call(t, s, tok.AccessToken)
	if resp.StatusCode != 403 || !strings.Contains(resp.Header.Get("WWW-Authenticate"), `scope="read write"`) {
		t.Errorf("missing scope: %d %q", resp.StatusCode, resp.Header.Get("WWW-Authenticate"))
	}
	s.Require()
	if resp := call(t, s, tok.AccessToken); resp.StatusCode != 200 {
		t.Errorf("call: %d", resp.StatusCode)
	}
	s.Revoke(tok.AccessToken)
	resp = call(t, s, tok.AccessToken)
	if resp.StatusCode != 401 || !strings.Contains(resp.Header.Get("WWW-Authenticate"), s.ResourceMetadataURL()) {
		t.Errorf("revoked: %d %q", resp.StatusCode, resp.Header.Get("WWW-Authenticate"))
	}
}
