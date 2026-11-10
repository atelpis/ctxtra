package oauth

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/oauthex"
	"github.com/zalando/go-keyring"

	"github.com/ctxtra/ctxtra/internal/mcp/creds"
	"github.com/ctxtra/ctxtra/internal/mcp/oauthtest"
	"github.com/ctxtra/ctxtra/internal/mcp/upstream"
)

func TestMain(m *testing.M) {
	creds.MockKeychain(nil)
	os.Exit(m.Run())
}

// browser follows the authorization URL to the redirect, as a browser
// that is signed in would; edit can change the redirect first.
func browser(fake *oauthtest.Server, edit func(url.Values)) func(string) error {
	return func(authURL string) error {
		c := *fake.Client()
		c.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
		resp, err := c.Get(authURL)
		if err != nil {
			return err
		}
		resp.Body.Close()
		loc, err := url.Parse(resp.Header.Get("Location"))
		if err != nil {
			return err
		}
		if edit != nil {
			q := loc.Query()
			edit(q)
			loc.RawQuery = q.Encode()
		}
		go func() {
			if resp, err := http.Get(loc.String()); err == nil {
				resp.Body.Close()
			}
		}()
		return nil
	}
}

type syncBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuf) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuf) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

func setup(t *testing.T, o oauthtest.Options) (*oauthtest.Server, *SignIn) {
	t.Helper()
	keyring.MockInit()
	fake := oauthtest.New(t, o)
	store := &creds.Store{Dir: t.TempDir(), HTTP: fake.Client()}
	if _, err := store.EnsureKey(); err != nil {
		t.Fatal(err)
	}
	srv := upstream.Server{ID: "linear_work", Name: "linear_work", URL: fake.ResourceURL()}
	return fake, &SignIn{Server: srv, Store: store, HTTP: fake.Client(), Out: &syncBuf{},
		OpenBrowser: browser(fake, nil), Timeout: 10 * time.Second}
}

func challenge(t *testing.T, f *SignIn) *Challenge {
	t.Helper()
	ch, err := Detect(context.Background(), f.HTTP, f.Server.URL, nil)
	if err != nil || ch == nil {
		t.Fatalf("Detect: %v %v", ch, err)
	}
	return ch
}

func stored(t *testing.T, f *SignIn) *creds.Record {
	t.Helper()
	r, err := f.Store.Load(f.Server)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestSignIn(t *testing.T) {
	fake, f := setup(t, oauthtest.Options{Iss: true, Lifetime: time.Hour, Scopes: []string{"read", "offline_access"},
		Account: "me@example.com"})
	res, err := f.Run(context.Background(), challenge(t, f))
	if err != nil {
		t.Fatal(err)
	}
	if res.Issuer != fake.URL || res.Account != "me@example.com" || res.NoRefresh {
		t.Errorf("result: %+v", res)
	}
	r := stored(t, f)
	if r.Issuer != fake.URL || !r.Client.Registered || r.Token.Refresh == "" || strings.Join(r.Token.Scopes, " ") != "offline_access read" {
		t.Errorf("record: %+v", r)
	}
	tok, err := f.Store.Token(context.Background(), f.Server)
	if err != nil {
		t.Fatal(err)
	}
	req, _ := http.NewRequest("POST", fake.ResourceURL(), nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	if resp, err := fake.Client().Do(req); err != nil || resp.StatusCode != 200 {
		t.Errorf("a call with the token: %v %v", resp, err)
	}
}

func TestDetect(t *testing.T) {
	fake, f := setup(t, oauthtest.Options{})
	ch := challenge(t, f)
	if ch.ResourceMetadata != fake.ResourceMetadataURL() {
		t.Errorf("challenge: %+v", ch)
	}
	open := oauthtest.New(t, oauthtest.Options{})
	// A server that answers without a token needs no sign-in: the fake's
	// metadata document is such an endpoint.
	if ch, err := Detect(context.Background(), open.Client(), open.URL+"/.well-known/oauth-authorization-server", nil); ch != nil || err != nil {
		t.Errorf("no OAuth: %v %v", ch, err)
	}
}

// Sign-ins that must fail store nothing.
func TestSignInFails(t *testing.T) {
	for _, tc := range []struct {
		name string
		o    oauthtest.Options
		edit func(url.Values)
		want string
	}{
		{"wrong issuer", oauthtest.Options{WrongIssuer: true}, nil, "issuer"},
		{"no PKCE methods", oauthtest.Options{NoPKCE: true}, nil, "PKCE"},
		{"only plain PKCE", oauthtest.Options{PKCEMethods: []string{"plain"}}, nil, "S256"},
		{"wrong iss", oauthtest.Options{Iss: true}, func(q url.Values) { q.Set("iss", "https://evil.example") }, "iss"},
		{"missing iss", oauthtest.Options{Iss: true}, func(q url.Values) { q.Del("iss") }, "iss"},
		{"refused", oauthtest.Options{}, func(q url.Values) { q.Del("code"); q.Set("error", "access_denied") }, "access_denied"},
		{"no app", oauthtest.Options{NoDCR: true}, nil, "--client-id"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fake, f := setup(t, tc.o)
			f.OpenBrowser = browser(fake, tc.edit)
			_, err := f.Run(context.Background(), challenge(t, f))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("Run: %v", err)
			}
			if r := stored(t, f); r != nil {
				t.Errorf("stored: %+v", r)
			}
		})
	}
}

func TestPlainAndS256(t *testing.T) {
	fake, f := setup(t, oauthtest.Options{PKCEMethods: []string{"plain", "S256"}})
	var method string
	open := browser(fake, nil)
	f.OpenBrowser = func(u string) error {
		p, _ := url.Parse(u)
		method = p.Query().Get("code_challenge_method")
		return open(u)
	}
	if _, err := f.Run(context.Background(), challenge(t, f)); err != nil || method != "S256" {
		t.Errorf("Run: %v, method %q", err, method)
	}
}

// Without protected resource metadata, as Atlassian, the authorization
// server is found at the server's origin.
func TestOriginFallback(t *testing.T) {
	_, f := setup(t, oauthtest.Options{NoResourceMetadata: true})
	ch := challenge(t, f)
	if ch.ResourceMetadata != "" {
		t.Fatalf("challenge: %+v", ch)
	}
	if _, err := f.Run(context.Background(), ch); err != nil {
		t.Error(err)
	}
}

func TestHTTPEndpoints(t *testing.T) {
	_, f := setup(t, oauthtest.Options{HTTPEndpoints: true})
	m, err := Discover(context.Background(), f.HTTP, f.Server.URL, challenge(t, f))
	if err != nil {
		t.Fatalf("a loopback server may have loopback http endpoints: %v", err)
	}
	if err := checkEndpoints(m.Server, "https://mcp.example.com/mcp"); err == nil || !strings.Contains(err.Error(), "authorization endpoint") {
		t.Errorf("http endpoints for a remote server: %v", err)
	}
}

// ctxtra's metadata document isn't hosted yet: a server that supports it
// and dynamic registration gets a registration.
func TestCIMDOffWhileUnset(t *testing.T) {
	_, f := setup(t, oauthtest.Options{CIMD: true})
	if _, err := f.Run(context.Background(), challenge(t, f)); err != nil {
		t.Fatal(err)
	}
	if r := stored(t, f); !r.Client.Registered || strings.HasPrefix(r.Client.ID, "https://") {
		t.Errorf("client: %+v", r.Client)
	}
}

func TestOwnAppWithSecret(t *testing.T) {
	_, f := setup(t, oauthtest.Options{NoDCR: true, Clients: map[string]string{"app": "s3cret"}})
	f.Server.ClientID, f.ClientSecret = "app", "s3cret"
	if _, err := f.Run(context.Background(), challenge(t, f)); err != nil {
		t.Fatal(err)
	}
	if r := stored(t, f); r.Client.ID != "app" || r.Client.Secret != "s3cret" || r.Client.AuthMethod != "client_secret_basic" {
		t.Errorf("client: %+v", r.Client)
	}
}

func TestCallbackPortAndRegistrations(t *testing.T) {
	fake, f := setup(t, oauthtest.Options{StrictRedirect: true})
	f.CallbackPort = freePort(t)
	if _, err := f.Run(context.Background(), challenge(t, f)); err != nil {
		t.Fatal(err)
	}
	first := stored(t, f).Client
	if !strings.Contains(first.Redirect, ":"+itoa(f.CallbackPort)+"/") {
		t.Errorf("redirect %s", first.Redirect)
	}
	// The same port reuses the registration.
	if _, err := f.Run(context.Background(), challenge(t, f)); err != nil {
		t.Fatal(err)
	}
	if again := stored(t, f).Client; again.ID != first.ID {
		t.Errorf("registered again for the same redirect: %s, %s", first.ID, again.ID)
	}
	// Another port: the stored registration's redirect wouldn't be
	// accepted, so it registers again.
	f.CallbackPort = 0
	f.OpenBrowser = browser(fake, nil)
	if _, err := f.Run(context.Background(), challenge(t, f)); err != nil {
		t.Fatal(err)
	}
	if moved := stored(t, f).Client; moved.ID == first.ID {
		t.Errorf("kept a registration for another redirect: %+v", moved)
	}
}

func TestWrongStateDoesntEndSignIn(t *testing.T) {
	fake, f := setup(t, oauthtest.Options{})
	open := browser(fake, nil)
	f.OpenBrowser = func(u string) error {
		p, _ := url.Parse(u)
		redirect := p.Query().Get("redirect_uri")
		resp, err := http.Get(redirect + "?state=wrong&code=x")
		if err != nil {
			return err
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("wrong state: %d", resp.StatusCode)
		}
		return open(u)
	}
	if _, err := f.Run(context.Background(), challenge(t, f)); err != nil {
		t.Error(err)
	}
}

// Over SSH: the URL is printed, and the redirect the browser ends at is
// pasted.
func TestPastedRedirect(t *testing.T) {
	fake, f := setup(t, oauthtest.Options{})
	f.NoBrowser = true
	in, paste := io.Pipe()
	f.In = in
	out := f.Out.(*syncBuf)
	done := make(chan error)
	go func() {
		_, err := f.Run(context.Background(), challenge(t, f))
		done <- err
	}()
	re := regexp.MustCompile(`https://\S+/authorize\?\S+`)
	var authURL string
	for authURL == "" {
		time.Sleep(10 * time.Millisecond)
		authURL = re.FindString(out.String())
	}
	c := *fake.Client()
	c.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := c.Get(authURL)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	io.WriteString(paste, resp.Header.Get("Location")+"\n")
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if r := stored(t, f); r == nil || r.Token.Access == "" {
		t.Errorf("record: %+v", r)
	}
}

func TestTimeout(t *testing.T) {
	_, f := setup(t, oauthtest.Options{})
	f.OpenBrowser = func(string) error { return nil }
	f.Timeout = 100 * time.Millisecond
	if _, err := f.Run(context.Background(), challenge(t, f)); err == nil || !strings.Contains(err.Error(), "no sign-in came back") {
		t.Errorf("Run: %v", err)
	}
}

func TestSecondAccountHint(t *testing.T) {
	fake, f := setup(t, oauthtest.Options{})
	if _, err := f.Run(context.Background(), challenge(t, f)); err != nil {
		t.Fatal(err)
	}
	second := *f
	second.Server = upstream.Server{ID: "linear_ctxtra", Name: "linear_ctxtra", URL: fake.ResourceURL()}
	out := &syncBuf{}
	second.Out = out
	if _, err := second.Run(context.Background(), challenge(t, &second)); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "already signed in to "+fake.URL+" as linear_work") || !strings.Contains(out.String(), "private window") {
		t.Errorf("output: %s", out.String())
	}
	if strings.Contains(f.Out.(*syncBuf).String(), "private window") {
		t.Error("a first sign-in got the hint")
	}
}

func TestNoRefreshToken(t *testing.T) {
	_, f := setup(t, oauthtest.Options{NoRefresh: true, Lifetime: time.Hour})
	res, err := f.Run(context.Background(), challenge(t, f))
	if err != nil || !res.NoRefresh {
		t.Errorf("Run: %+v %v", res, err)
	}
}

func TestScopes(t *testing.T) {
	m := &Metadata{ResourceScopes: []string{"prm"}, Server: &oauthex.AuthServerMeta{ScopesSupported: []string{"offline_access"}}}
	for _, tc := range []struct {
		configured []string
		ch         *Challenge
		extra      []string
		want       string
	}{
		{[]string{"cfg"}, &Challenge{Scopes: []string{"ch"}}, nil, "cfg offline_access"},
		{nil, &Challenge{Scopes: []string{"ch"}}, []string{"stepup"}, "ch offline_access stepup"},
		{nil, nil, nil, "offline_access prm"},
	} {
		if got := strings.Join(Scopes(tc.configured, tc.ch, m, tc.extra), " "); got != tc.want {
			t.Errorf("Scopes(%v, %v, %v) = %s, want %s", tc.configured, tc.ch, tc.extra, got, tc.want)
		}
	}
}

func TestAccount(t *testing.T) {
	if account(nil) != "" {
		t.Error("no ID token")
	}
	if got := account(map[string]json.RawMessage{"id_token": json.RawMessage(`"not-a-jwt"`)}); got != "" {
		t.Errorf("a bad ID token: %q", got)
	}
}

func freePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port
}

func itoa(n int) string { return strconv.Itoa(n) }

// A 401 without a Bearer challenge is about the server's own key: no
// OAuth. An SSE server is probed with GET.
func TestDetectKinds(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/basic":
			w.Header().Set("WWW-Authenticate", `Basic realm="x"`)
			w.WriteHeader(http.StatusUnauthorized)
		case r.URL.Path == "/sse" && r.Method == http.MethodPost:
			w.WriteHeader(http.StatusMethodNotAllowed)
		case r.URL.Path == "/sse":
			w.Header().Set("WWW-Authenticate", `Bearer resource_metadata="https://x/.well-known/oauth-protected-resource"`)
			w.WriteHeader(http.StatusUnauthorized)
		}
	}))
	defer srv.Close()
	if ch, err := Detect(context.Background(), srv.Client(), srv.URL+"/basic", nil); ch != nil || err != nil {
		t.Errorf("Basic: %+v %v", ch, err)
	}
	if ch, err := Detect(context.Background(), srv.Client(), srv.URL+"/sse", nil); ch == nil || err != nil || ch.ResourceMetadata == "" {
		t.Errorf("SSE: %+v %v", ch, err)
	}
}

// A registration answer that can't be read doesn't reach the error text.
func TestRegistrationErrorQuotesNothing(t *testing.T) {
	reg := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		io.WriteString(w, `{"client_id": 7, "client_secret": "CTXMARK-SECRET"}`)
	}))
	defer reg.Close()
	f := &SignIn{Server: upstream.Server{ID: "x", Name: "x", URL: "https://mcp.example/mcp"}, Store: &creds.Store{Dir: t.TempDir()}, HTTP: reg.Client()}
	m := &Metadata{Server: &oauthex.AuthServerMeta{RegistrationEndpoint: reg.URL + "/register", TokenEndpoint: reg.URL + "/token"}}
	_, err := f.client(context.Background(), m, "http://127.0.0.1:1/callback")
	if err == nil || strings.Contains(err.Error(), "CTXMARK") {
		t.Errorf("client: %v", err)
	}
}

// A sign-in whose server was removed meanwhile saves nothing.
func TestCommitRefuses(t *testing.T) {
	_, f := setup(t, oauthtest.Options{})
	f.Commit = func(func() error) error { return errors.New("linear_work was removed") }
	if _, err := f.Run(context.Background(), challenge(t, f)); err == nil {
		t.Error("Run succeeded")
	}
	if r := stored(t, f); r != nil {
		t.Errorf("stored: %+v", r)
	}
}
