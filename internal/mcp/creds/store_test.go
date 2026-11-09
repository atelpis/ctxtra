package creds

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/zalando/go-keyring"

	"github.com/ctxtra/ctxtra/internal/fsutil"
	"github.com/ctxtra/ctxtra/internal/mcp/oauthtest"
	"github.com/ctxtra/ctxtra/internal/mcp/upstream"
)

// signedInStore is a store with a sign-in of a server at fake.
func signedInStore(t *testing.T, fake *oauthtest.Server, dir string) (*Store, upstream.Server, oauthtest.Tokens) {
	t.Helper()
	keyring.MockInit()
	fake.AddClient("app", "")
	s := &Store{Dir: dir, HTTP: fake.Client()}
	if _, err := s.EnsureKey(); err != nil {
		t.Fatal(err)
	}
	srv := upstream.Server{ID: "linear_work", Name: "linear_work", URL: fake.ResourceURL()}
	tok := fake.SignIn("app", "read")
	saveSignIn(t, s, srv, fake, tok)
	return s, srv, tok
}

func saveSignIn(t *testing.T, s *Store, srv upstream.Server, fake *oauthtest.Server, tok oauthtest.Tokens) {
	t.Helper()
	tr := tokenResponse{AccessToken: tok.AccessToken, RefreshToken: tok.RefreshToken, TokenType: tok.TokenType,
		ExpiresIn: json.Number(itoa(tok.ExpiresIn)), Scope: tok.Scope}
	c := Client{ID: "app", AuthMethod: "none", TokenURL: fake.TokenURL()}
	gen, err := s.Generation(srv)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.SaveSignIn(srv, gen, fake.URL, c, newToken(tr, time.Now())); err != nil {
		t.Fatal(err)
	}
}

func itoa(n int) string {
	b, _ := json.Marshal(n)
	return string(b)
}

func TestRoundTrip(t *testing.T) {
	fake := oauthtest.New(t, oauthtest.Options{Lifetime: time.Hour})
	s, srv, tok := signedInStore(t, fake, t.TempDir())
	r, err := (&Store{Dir: s.Dir}).Load(srv)
	if err != nil {
		t.Fatal(err)
	}
	if r.ID != srv.ID || r.URL != srv.URL || r.Issuer != fake.URL || r.Generation <= 0 ||
		r.Token.Access != tok.AccessToken || r.Token.Refresh != tok.RefreshToken || r.Client.ID != "app" {
		t.Errorf("record: %+v", r)
	}
	if got, err := s.Token(context.Background(), srv); err != nil || got != tok.AccessToken {
		t.Errorf("Token: %q %v", got, err)
	}
	if fake.Refreshes() != 0 {
		t.Error("a valid token was refreshed")
	}
}

func TestFailedSync(t *testing.T) {
	keyring.MockInit()
	s := &Store{Dir: t.TempDir()}
	if _, err := s.EnsureKey(); err != nil {
		t.Fatal(err)
	}
	setSecret(t, s, local, "old")
	old := syncFile
	t.Cleanup(func() { syncFile = old })
	syncFile = func(*os.File) error { return errors.New("I/O error") }
	err := s.SetSecrets(local, map[string]string{"PGPASSWORD": "new"}, nil)
	if err == nil || !strings.Contains(err.Error(), "could not be saved") {
		t.Errorf("SetSecrets: %v", err)
	}
	syncFile = old
	if v, err := secret(t, s, local); err != nil || v != "old" {
		t.Errorf("secret: %q %v", v, err)
	}
	if tmp, _ := filepath.Glob(filepath.Join(s.Dir, "credentials", ".tmp-*")); len(tmp) > 0 {
		t.Errorf("temporary files: %v", tmp)
	}
}

func TestNewerFormat(t *testing.T) {
	fake := oauthtest.New(t, oauthtest.Options{Lifetime: time.Second})
	s, srv, _ := signedInStore(t, fake, t.TempDir())
	newer := filepath.Join(s.Dir, "credentials", "linear_work.v2.age")
	os.WriteFile(newer, []byte("v2"), 0o600)
	before, _ := os.ReadFile(s.recordPath(srv.ID, 1))
	var nerr *NewerError
	if _, err := s.Token(context.Background(), srv); !errors.As(err, &nerr) || !strings.Contains(err.Error(), "upgrade ctxtra") {
		t.Errorf("Token: %v", err)
	}
	if _, err := s.Update(srv, true, func(*Record) error { return nil }); !errors.As(err, &nerr) {
		t.Errorf("Update: %v", err)
	}
	if err := s.Delete(srv); !errors.As(err, &nerr) {
		t.Errorf("Delete: %v", err)
	}
	after, _ := os.ReadFile(s.recordPath(srv.ID, 1))
	if !bytes.Equal(before, after) || fake.Refreshes() != 0 {
		t.Error("the store changed")
	}
	if _, err := os.Stat(newer); err != nil {
		t.Error(err)
	}
}

func TestWriterDeletesOlderVersions(t *testing.T) {
	keyring.MockInit()
	s := &Store{Dir: t.TempDir()}
	if _, err := s.EnsureKey(); err != nil {
		t.Fatal(err)
	}
	os.MkdirAll(filepath.Join(s.Dir, "credentials"), 0o700)
	older := filepath.Join(s.Dir, "credentials", "pg.v0.age")
	os.WriteFile(older, []byte("v0"), 0o600)
	setSecret(t, s, local, "pw")
	if _, err := os.Stat(older); !os.IsNotExist(err) {
		t.Errorf("older version kept: %v", err)
	}
}

func TestOlderWriterKeepsNewerFields(t *testing.T) {
	fake := oauthtest.New(t, oauthtest.Options{Lifetime: time.Second})
	s, srv, _ := signedInStore(t, fake, t.TempDir())
	// A newer ctxtra added fields, at the top and in the token.
	if _, err := s.Update(srv, false, func(r *Record) error {
		var m map[string]any
		json.Unmarshal(r.raw, &m)
		m["account"] = map[string]any{"email": "a@example.com"}
		m["token"].(map[string]any)["id_token"] = "idt"
		m["generation"] = 7
		r.raw, _ = json.Marshal(m)
		r.Generation = 7
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	expireToken(t, s, srv, time.Minute)
	if _, err := s.Token(context.Background(), srv); err != nil {
		t.Fatal(err)
	}
	r, err := s.Load(srv)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	json.Unmarshal(r.raw, &m)
	if m["account"] == nil || m["token"].(map[string]any)["id_token"] != "idt" || r.Generation != 7 || fake.Refreshes() != 1 {
		t.Errorf("record after refresh: %s", r.raw)
	}
}

func TestChangedURL(t *testing.T) {
	fake := oauthtest.New(t, oauthtest.Options{Lifetime: time.Hour})
	s, srv, _ := signedInStore(t, fake, t.TempDir())
	moved := srv
	moved.URL = "https://elsewhere.example/mcp"
	var lerr *LoginError
	if _, err := s.Token(context.Background(), moved); !errors.As(err, &lerr) || !strings.Contains(err.Error(), "run ctxtra mcp login linear_work") {
		t.Errorf("Token: %v", err)
	}
	if _, err := s.Rejected(context.Background(), moved, "x"); !errors.As(err, &lerr) || fake.Refreshes() != 0 {
		t.Errorf("Rejected: %v, %d refreshes", err, fake.Refreshes())
	}
	// Spelling differences of the same URL are the same server.
	same := srv
	same.URL = strings.Replace(srv.URL, "https://127.0.0.1", "HTTPS://127.0.0.1", 1) + "#x"
	if _, err := s.Token(context.Background(), same); err != nil {
		t.Errorf("same URL: %v", err)
	}
}

// Two configs with a server of the same name share its sign-in when the
// URLs are the same, and never send it to another URL.
func TestSameNameInTwoConfigs(t *testing.T) {
	fake := oauthtest.New(t, oauthtest.Options{Lifetime: time.Hour})
	dir := t.TempDir()
	s, srv, tok := signedInStore(t, fake, dir)
	other := &Store{Dir: dir, HTTP: fake.Client()}
	if got, err := other.Token(context.Background(), srv); err != nil || got != tok.AccessToken {
		t.Errorf("same URL: %q %v", got, err)
	}
	diff := srv
	diff.URL = "https://mcp.linear.example/mcp"
	if got, err := other.Token(context.Background(), diff); err == nil || got != "" {
		t.Errorf("another URL: %q %v", got, err)
	}
	_ = s
}

func TestStateHasNoSecrets(t *testing.T) {
	fake := oauthtest.New(t, oauthtest.Options{Lifetime: time.Second})
	s, srv, tok := signedInStore(t, fake, t.TempDir())
	setSecret(t, s, local, "pw-secret")
	got, err := s.Token(context.Background(), srv)
	if err != nil {
		t.Fatal(err)
	}
	r, _ := s.Load(srv)
	for _, id := range []string{"linear_work", "pg"} {
		data, err := os.ReadFile(filepath.Join(s.Dir, "state", id+".json"))
		if err != nil {
			t.Fatal(err)
		}
		for _, v := range []string{tok.AccessToken, tok.RefreshToken, got, r.Token.Refresh, "pw-secret"} {
			if bytes.Contains(data, []byte(v)) {
				t.Errorf("state of %s has a secret: %s", id, data)
			}
		}
	}
	st, _ := s.State(srv)
	if !st.SignedIn || st.Generation != r.Generation || st.Version != 1 {
		t.Errorf("state: %+v", st)
	}
}

func TestFileID(t *testing.T) {
	for in, want := range map[string]string{"linear_work": "linear_work", "work/linear": "work%2Flinear",
		"Work": "%57ork", "a.b": "a%2Eb", "50%": "50%25"} {
		if got := fileID(in); got != want {
			t.Errorf("fileID(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestCanonicalURL(t *testing.T) {
	for in, want := range map[string]string{
		"HTTPS://MCP.Linear.app:443/mcp#frag": "https://mcp.linear.app/mcp",
		"https://user:pw@host/x?q=1":          "https://host/x?q=1",
		"http://127.0.0.1:80/mcp":             "http://127.0.0.1/mcp",
		"https://[::1]:8443/mcp":              "https://[::1]:8443/mcp",
	} {
		if got := CanonicalURL(in); got != want {
			t.Errorf("CanonicalURL(%q) = %q, want %q", in, got, want)
		}
	}
}

func expireToken(t *testing.T, s *Store, srv upstream.Server, d time.Duration) {
	t.Helper()
	if _, err := s.Update(srv, false, func(r *Record) error {
		r.Token.Obtained, r.Token.Expiry = time.Now().Add(-time.Hour), time.Now().Add(d)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestTwoSessionsRefreshAtOnce(t *testing.T) {
	fake := oauthtest.New(t, oauthtest.Options{Lifetime: time.Hour, RefreshDelay: 100 * time.Millisecond})
	s, srv, tok := signedInStore(t, fake, t.TempDir())
	expireToken(t, s, srv, time.Minute)
	got := make([]string, 4)
	var wg sync.WaitGroup
	for i := range got {
		wg.Go(func() {
			var err error
			got[i], err = (&Store{Dir: s.Dir, HTTP: fake.Client()}).Token(context.Background(), srv)
			if err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	for _, g := range got {
		if g != got[0] || g == tok.AccessToken {
			t.Errorf("tokens: %v", got)
		}
	}
	if fake.Refreshes() != 1 {
		t.Errorf("refreshes: %d", fake.Refreshes())
	}
	// The next refresh works: the rotated refresh token was saved.
	expireToken(t, s, srv, time.Minute)
	if _, err := s.Token(context.Background(), srv); err != nil || fake.Refreshes() != 2 {
		t.Errorf("next refresh: %v, %d", err, fake.Refreshes())
	}
}

func TestRefreshMargin(t *testing.T) {
	fake := oauthtest.New(t, oauthtest.Options{Lifetime: time.Hour})
	s, srv, _ := signedInStore(t, fake, t.TempDir())
	// A 4-minute token with 3 minutes left: within 5 minutes, but not
	// within half its lifetime.
	s.Update(srv, false, func(r *Record) error {
		r.Token.Obtained, r.Token.Expiry = time.Now().Add(-time.Minute), time.Now().Add(3*time.Minute)
		return nil
	})
	if _, err := s.Token(context.Background(), srv); err != nil || fake.Refreshes() != 0 {
		t.Errorf("4-minute token: %v, %d refreshes", err, fake.Refreshes())
	}
	expireToken(t, s, srv, 4*time.Minute)
	if _, err := s.Token(context.Background(), srv); err != nil || fake.Refreshes() != 1 {
		t.Errorf("token with 4 minutes left: %v, %d refreshes", err, fake.Refreshes())
	}
}

func TestLockTimeout(t *testing.T) {
	fake := oauthtest.New(t, oauthtest.Options{Lifetime: time.Hour})
	s, srv, _ := signedInStore(t, fake, t.TempDir())
	expireToken(t, s, srv, time.Minute)
	unlock, err := fsutil.Lock(s.lockPath(fileID(srv.ID)))
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	s.LockTimeout = 100 * time.Millisecond
	if _, err := s.Token(context.Background(), srv); err == nil || !strings.Contains(err.Error(), "try again") {
		t.Errorf("Token: %v", err)
	}
	if fake.Refreshes() != 0 {
		t.Error("refreshed without the lock")
	}
	if refreshTimeout >= 30*time.Second {
		t.Error("a refresh request must time out before the lock wait")
	}
}

func TestLoginDuringRefresh(t *testing.T) {
	fake := oauthtest.New(t, oauthtest.Options{Lifetime: time.Hour, RefreshDelay: 300 * time.Millisecond})
	s, srv, _ := signedInStore(t, fake, t.TempDir())
	expireToken(t, s, srv, time.Minute)
	done := make(chan error)
	go func() {
		_, err := (&Store{Dir: s.Dir, HTTP: fake.Client()}).Token(context.Background(), srv)
		done <- err
	}()
	time.Sleep(100 * time.Millisecond)
	before, _ := s.Generation(srv)
	login := fake.SignIn("app", "read", "write")
	saveSignIn(t, s, srv, fake, login)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	r, _ := s.Load(srv)
	if r.Token.Access != login.AccessToken || r.Generation <= before {
		t.Errorf("the new sign-in was lost: %+v", r.Token)
	}
	if got, err := s.Token(context.Background(), srv); err != nil || got != login.AccessToken {
		t.Errorf("Token: %q %v", got, err)
	}
}

func TestRemoveDuringRefresh(t *testing.T) {
	fake := oauthtest.New(t, oauthtest.Options{Lifetime: time.Hour, RefreshDelay: 300 * time.Millisecond})
	s, srv, _ := signedInStore(t, fake, t.TempDir())
	expireToken(t, s, srv, time.Minute)
	done := make(chan error)
	go func() {
		_, err := (&Store{Dir: s.Dir, HTTP: fake.Client()}).Token(context.Background(), srv)
		done <- err
	}()
	time.Sleep(100 * time.Millisecond)
	if err := s.Delete(srv); err != nil {
		t.Fatal(err)
	}
	<-done
	if r, err := s.Load(srv); r != nil || err != nil {
		t.Errorf("the record came back: %v %v", r, err)
	}
	// A refresh that starts after the removal writes nothing either.
	if _, err := s.Rejected(context.Background(), srv, "x"); !errors.As(err, new(*LoginError)) {
		t.Errorf("Rejected: %v", err)
	}
	if _, err := s.Update(srv, false, func(*Record) error { return nil }); !errors.Is(err, ErrDeleted) {
		t.Errorf("Update: %v", err)
	}
	if files, _ := filepath.Glob(filepath.Join(s.Dir, "credentials", "*.age")); len(files) > 0 {
		t.Errorf("files: %v", files)
	}
}

func TestRejectedToken(t *testing.T) {
	fake := oauthtest.New(t, oauthtest.Options{})
	s, srv, tok := signedInStore(t, fake, t.TempDir())
	fake.Revoke(tok.AccessToken)
	next, err := s.Rejected(context.Background(), srv, tok.AccessToken)
	if err != nil || next == tok.AccessToken {
		t.Fatalf("Rejected: %q %v", next, err)
	}
	// Another session that got the same 401 uses the stored token.
	if again, err := (&Store{Dir: s.Dir, HTTP: fake.Client()}).Rejected(context.Background(), srv, tok.AccessToken); err != nil || again != next {
		t.Errorf("second Rejected: %q %v", again, err)
	}
	if fake.Refreshes() != 1 {
		t.Errorf("refreshes: %d", fake.Refreshes())
	}
}

func TestInvalidGrant(t *testing.T) {
	fake := oauthtest.New(t, oauthtest.Options{Lifetime: time.Hour})
	s, srv, _ := signedInStore(t, fake, t.TempDir())
	s.Update(srv, false, func(r *Record) error {
		r.Token.Refresh = "dead"
		return nil
	})
	expireToken(t, s, srv, time.Minute)
	_, err := s.Token(context.Background(), srv)
	if err == nil || err.Error() != "linear_work: the sign-in expired or was revoked; run ctxtra mcp login linear_work" {
		t.Errorf("Token: %v", err)
	}
	if st, _ := s.State(srv); !st.NeedsLogin || st.SignedIn {
		t.Errorf("state: %+v", st)
	}
}

func TestTokensWithoutRefreshToken(t *testing.T) {
	fake := oauthtest.New(t, oauthtest.Options{NoRefresh: true, Lifetime: time.Hour})
	s, srv, tok := signedInStore(t, fake, t.TempDir())
	expireToken(t, s, srv, time.Minute)
	if _, err := s.Token(context.Background(), srv); err == nil || !strings.Contains(err.Error(), "the sign-in expired; run ctxtra mcp login") {
		t.Errorf("Token: %v", err)
	}
	if st, _ := s.State(srv); !st.NeedsLogin {
		t.Errorf("state: %+v", st)
	}

	// Without an expiry, the token is used until the server rejects it.
	forever := oauthtest.New(t, oauthtest.Options{NoRefresh: true})
	s, srv, tok = signedInStore(t, forever, t.TempDir())
	if got, err := s.Token(context.Background(), srv); err != nil || got != tok.AccessToken {
		t.Errorf("Token: %q %v", got, err)
	}
	if _, err := s.Rejected(context.Background(), srv, tok.AccessToken); err == nil || !strings.Contains(err.Error(), "run ctxtra mcp login") {
		t.Errorf("Rejected: %v", err)
	}
}

func TestRefreshKeepsRefreshToken(t *testing.T) {
	fake := oauthtest.New(t, oauthtest.Options{KeepRefresh: true, Lifetime: time.Hour})
	s, srv, tok := signedInStore(t, fake, t.TempDir())
	expireToken(t, s, srv, time.Minute)
	if _, err := s.Token(context.Background(), srv); err != nil {
		t.Fatal(err)
	}
	if r, _ := s.Load(srv); r.Token.Refresh != tok.RefreshToken || r.Token.Scopes[0] != "read" {
		t.Errorf("token: %+v", r.Token)
	}
}

func TestSaveBeforeUse(t *testing.T) {
	fake := oauthtest.New(t, oauthtest.Options{Lifetime: time.Hour})
	s, srv, _ := signedInStore(t, fake, t.TempDir())
	expireToken(t, s, srv, time.Minute)
	old := syncFile
	t.Cleanup(func() { syncFile = old })
	syncFile = func(*os.File) error { return errors.New("no space left on device") }
	got, err := s.Token(context.Background(), srv)
	if got != "" || err == nil || !strings.Contains(err.Error(), "could not be saved") {
		t.Errorf("Token: %q %v", got, err)
	}
}

func TestHTTPTokenEndpoint(t *testing.T) {
	r := &Record{URL: "https://mcp.example/mcp", Client: Client{TokenURL: "http://auth.example/token"}, Token: Token{Refresh: "r"}}
	if _, err := (&Store{}).refresh(context.Background(), r); err == nil || !strings.Contains(err.Error(), "HTTPS") {
		t.Errorf("refresh: %v", err)
	}
	if err := checkEndpoint("http://127.0.0.1:9/token", "http://127.0.0.1:8/mcp"); err != nil {
		t.Errorf("loopback: %v", err)
	}
}

func TestStaleSignIn(t *testing.T) {
	fake := oauthtest.New(t, oauthtest.Options{Lifetime: time.Hour})
	s, srv, _ := signedInStore(t, fake, t.TempDir())
	c := Client{ID: "app", AuthMethod: "none", TokenURL: fake.TokenURL()}
	gen, _ := s.Generation(srv)
	// Two sign-ins start from gen; the second to finish is too late.
	first, second := fake.SignIn("app"), fake.SignIn("app")
	if _, err := s.SaveSignIn(srv, gen, fake.URL, c, Token{Access: first.AccessToken}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SaveSignIn(srv, gen, fake.URL, c, Token{Access: second.AccessToken}); !errors.Is(err, ErrSignInChanged) {
		t.Errorf("stale sign-in: %v", err)
	}
	r, _ := s.Load(srv)
	if r.Token.Access != first.AccessToken || r.Generation <= gen {
		t.Errorf("record: %+v", r)
	}
	// A sign-in that started before a removal: not saved, whether or not
	// another sign-in came after the removal.
	pending := r.Generation
	if err := s.Delete(srv); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SaveSignIn(srv, pending, fake.URL, c, Token{Access: second.AccessToken}); !errors.Is(err, ErrSignInChanged) {
		t.Errorf("sign-in after removal: %v", err)
	}
	if r, err := s.Load(srv); r != nil || err != nil {
		t.Errorf("recreated: %+v %v", r, err)
	}
	fresh := fake.SignIn("app")
	if _, err := s.SaveSignIn(srv, 0, fake.URL, c, Token{Access: fresh.AccessToken}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SaveSignIn(srv, pending, fake.URL, c, Token{Access: second.AccessToken}); !errors.Is(err, ErrSignInChanged) {
		t.Errorf("sign-in from before a removal and a new sign-in: %v", err)
	}
	if r, _ := s.Load(srv); r.Token.Access != fresh.AccessToken {
		t.Errorf("the new sign-in was overwritten: %+v", r.Token)
	}
}

// A late 401 for an old token doesn't hand out a newer token that has
// expired meanwhile: it is refreshed.
func TestDelayedRejection(t *testing.T) {
	fake := oauthtest.New(t, oauthtest.Options{Lifetime: time.Hour})
	s, srv, tok := signedInStore(t, fake, t.TempDir())
	expireToken(t, s, srv, time.Minute)
	s.Update(srv, false, func(r *Record) error { r.Token.Access = "expired-b"; return nil })
	got, err := s.Rejected(context.Background(), srv, tok.AccessToken)
	if err != nil || got == "expired-b" || fake.Refreshes() != 1 {
		t.Errorf("Rejected: %q %v, %d refreshes", got, err, fake.Refreshes())
	}
}

func TestGenerationErrors(t *testing.T) {
	fake := oauthtest.New(t, oauthtest.Options{})
	s, srv, _ := signedInStore(t, fake, t.TempDir())
	os.WriteFile(filepath.Join(s.Dir, "credentials", "linear_work.v2.age"), []byte("v2"), 0o600)
	var nerr *NewerError
	if _, err := s.Generation(srv); !errors.As(err, &nerr) {
		t.Errorf("Generation: %v", err)
	}
}

func TestRefreshRefusesRedirects(t *testing.T) {
	fake := oauthtest.New(t, oauthtest.Options{Lifetime: time.Hour})
	s, srv, _ := signedInStore(t, fake, t.TempDir())
	redirect := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, fake.TokenURL(), http.StatusTemporaryRedirect)
	}))
	defer redirect.Close()
	s.Update(srv, false, func(r *Record) error {
		r.Client.TokenURL = redirect.URL + "/token"
		return nil
	})
	expireToken(t, s, srv, time.Minute)
	s.HTTP = &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}}
	if _, err := s.Token(context.Background(), srv); err == nil || !strings.Contains(err.Error(), "HTTP 307") {
		t.Errorf("Token: %v", err)
	}
	if fake.Refreshes() != 0 {
		t.Error("the redirect was followed")
	}
}

func TestRefreshErrorsQuoteNoProviderText(t *testing.T) {
	keyring.MockInit()
	srvHTTP := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "bad token " + r.PostForm.Get("refresh_token")})
	}))
	defer srvHTTP.Close()
	s := &Store{Dir: t.TempDir(), HTTP: srvHTTP.Client()}
	if _, err := s.EnsureKey(); err != nil {
		t.Fatal(err)
	}
	srv := upstream.Server{ID: "x", Name: "x", URL: "https://mcp.example/mcp"}
	c := Client{ID: "app", TokenURL: srvHTTP.URL + "/token"}
	if _, err := s.SaveSignIn(srv, 0, srvHTTP.URL, c, Token{Access: "a", Refresh: "CTXMARK-RT", Expiry: time.Now()}); err != nil {
		t.Fatal(err)
	}
	_, err := s.Token(context.Background(), srv)
	if err == nil || strings.Contains(err.Error(), "CTXMARK") || !strings.Contains(err.Error(), "HTTP 400") {
		t.Errorf("Token: %v", err)
	}
}
