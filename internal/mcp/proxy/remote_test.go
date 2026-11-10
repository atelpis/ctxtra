//go:build unix

package proxy

import (
	"context"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/ctxtra/ctxtra/internal/mcp/creds"
	"github.com/ctxtra/ctxtra/internal/mcp/oauthtest"
	"github.com/ctxtra/ctxtra/internal/mcp/upstream"
)

// mcpServer is an MCP server with one tool, echo.
func mcpServer() *mcp.Server {
	srv := mcp.NewServer(&mcp.Implementation{Name: "remote", Version: "1"}, nil)
	mcp.AddTool(srv, &mcp.Tool{Name: "echo"}, func(_ context.Context, _ *mcp.CallToolRequest, in map[string]any) (*mcp.CallToolResult, any, error) {
		b, _ := json.Marshal(in)
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: string(b)}}}, nil, nil
	})
	return srv
}

func streamable() http.Handler {
	srv := mcpServer()
	return mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return srv }, nil)
}

// trustCert makes the proxy trust a test server's certificate, through
// SSL_CERT_FILE as for a company's own certificate.
func trustCert(t *testing.T, srv *httptest.Server) {
	t.Helper()
	f := filepath.Join(t.TempDir(), "ca.pem")
	os.WriteFile(f, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: srv.Certificate().Raw}), 0o600)
	t.Setenv("SSL_CERT_FILE", f)
}

type remoteSetup struct {
	fake  *oauthtest.Server
	store *creds.Store
	srv   upstream.Server
}

func newRemote(t *testing.T, o oauthtest.Options, signIn bool) *remoteSetup {
	t.Helper()
	creds.MockKeychain(nil)
	o.Resource = streamable()
	fake := oauthtest.New(t, o)
	trustCert(t, fake.Server)
	fake.AddClient("app", "")
	store := &creds.Store{Dir: t.TempDir(), HTTP: fake.Client()}
	if _, err := store.EnsureKey(); err != nil {
		t.Fatal(err)
	}
	r := &remoteSetup{fake, store, upstream.Server{ID: "linear", Name: "linear", URL: fake.ResourceURL()}}
	if signIn {
		r.signIn(t)
	}
	return r
}

func (r *remoteSetup) signIn(t *testing.T) {
	t.Helper()
	tok := r.fake.SignIn("app", "read")
	gen, _ := r.store.Generation(r.srv)
	c := creds.Client{ID: "app", AuthMethod: "none", TokenURL: r.fake.TokenURL()}
	if _, err := r.store.SaveSignIn(r.srv, gen, r.fake.URL, c, creds.Token{Access: tok.AccessToken, Refresh: tok.RefreshToken}); err != nil {
		t.Fatal(err)
	}
}

func (r *remoteSetup) start(t *testing.T) *session {
	return startWith(t, t.TempDir(), 5*time.Second, func(o *Options) {
		o.Tokens, o.Secrets = r.store, r.store
		o.SignInState, o.SignInPoll = r.store.SignInState, 50*time.Millisecond
	}, r.srv)
}

func TestRemoteServer(t *testing.T) {
	r := newRemote(t, oauthtest.Options{}, true)
	s := r.start(t)
	if got := toolNames(s.tools(t)); !slices.Equal(got, []string{"linear__echo"}) {
		t.Fatalf("tools %v", got)
	}
	if out, err := s.call(t, "linear__echo", map[string]any{"a": 1}); err != nil || out != `{"a":1}` {
		t.Errorf("call: %q %v", out, err)
	}
	// A token revoked early: one refresh, one retry.
	tok, _ := r.store.Token(context.Background(), r.srv)
	r.fake.Revoke(tok)
	if out, err := s.call(t, "linear__echo", map[string]any{"b": 2}); err != nil || out != `{"b":2}` {
		t.Errorf("call after revoke: %q %v", out, err)
	}
	if r.fake.Refreshes() != 1 {
		t.Errorf("refreshes: %d", r.fake.Refreshes())
	}
}

// A dead refresh token fails the call with the login hint, and nothing
// opens a browser: the proxy has no way to.
func TestRemoteSignInGone(t *testing.T) {
	r := newRemote(t, oauthtest.Options{}, true)
	s := r.start(t)
	s.tools(t)
	tok, _ := r.store.Token(context.Background(), r.srv)
	r.store.Update(r.srv, false, func(rec *creds.Record) error { rec.Token.Refresh = "dead"; return nil })
	r.fake.Revoke(tok)
	out, err := s.call(t, "linear__echo", nil)
	if err == nil || !strings.Contains(out, "run ctxtra mcp login linear") {
		t.Errorf("call: %q %v", out, err)
	}
	if st, _ := r.store.State(r.srv); !st.NeedsLogin {
		t.Errorf("state: %+v", st)
	}
}

func TestRemoteStepUp(t *testing.T) {
	r := newRemote(t, oauthtest.Options{}, true)
	s := r.start(t)
	s.tools(t)
	r.fake.Require("read", "admin")
	out, err := s.call(t, "linear__echo", nil)
	if err == nil || !strings.Contains(out, "needs more permissions; run ctxtra mcp login linear") {
		t.Errorf("call: %q %v", out, err)
	}
	if st, _ := r.store.State(r.srv); !slices.Equal(st.Scopes, []string{"admin", "read"}) {
		t.Errorf("state scopes: %v", st.Scopes)
	}
	// A 401, a refresh, then a 403: the scopes are recorded too.
	r.fake.Require("write")
	tok, _ := r.store.Token(context.Background(), r.srv)
	r.fake.Revoke(tok)
	if out, err := s.call(t, "linear__echo", nil); err == nil || !strings.Contains(out, "needs more permissions") {
		t.Errorf("call after a refresh: %q %v", out, err)
	}
	if st, _ := r.store.State(r.srv); !slices.Contains(st.Scopes, "write") {
		t.Errorf("state scopes after a refresh: %v", st.Scopes)
	}
}

// A server that needs sign-in at the start gets its tools once the user
// signs in during the session.
func TestRemoteSignInDuringSession(t *testing.T) {
	r := newRemote(t, oauthtest.Options{}, false)
	s := r.start(t)
	if got := s.tools(t); len(got) != 0 {
		t.Fatalf("tools before sign-in: %v", toolNames(got))
	}
	if st, _ := r.store.State(r.srv); !st.NeedsLogin {
		t.Errorf("state: %+v", st)
	}
	r.signIn(t)
	select {
	case <-s.changed:
	case <-time.After(5 * time.Second):
		t.Fatal("no list change after sign-in")
	}
	if got := toolNames(s.tools(t)); !slices.Equal(got, []string{"linear__echo"}) {
		t.Errorf("tools after sign-in: %v", got)
	}
}

// A server without OAuth gets its secret header, and the older SSE
// transport works.
func TestRemoteSSEWithSecretHeader(t *testing.T) {
	creds.MockKeychain(nil)
	srv := mcpServer()
	sse := mcp.NewSSEHandler(func(*http.Request) *mcp.Server { return srv }, nil)
	var auth []string
	h := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		auth = append(auth, req.Header.Get("X-Api-Key"))
		if req.Method == http.MethodPost && req.URL.Query().Get("sessionid") == "" {
			// Not a streamable HTTP server.
			http.Error(w, "use SSE", http.StatusMethodNotAllowed)
			return
		}
		sse.ServeHTTP(w, req)
	}))
	t.Cleanup(h.Close)
	trustCert(t, h)
	store := &creds.Store{Dir: t.TempDir()}
	store.EnsureKey()
	rs := upstream.Server{ID: "old", Name: "old", URL: h.URL + "/sse", SecretHeaders: []string{"X-Api-Key"}}
	if err := store.SetSecrets(rs, nil, map[string]string{"X-Api-Key": "k"}); err != nil {
		t.Fatal(err)
	}
	s := startWith(t, t.TempDir(), 5*time.Second, func(o *Options) { o.Tokens, o.Secrets = store, store }, rs)
	if out, err := s.call(t, "old__echo", map[string]any{"x": true}); err != nil || out != `{"x":true}` {
		t.Errorf("call: %q %v; log %+v", out, err, s.records())
	}
	if !slices.Contains(auth, "k") || slices.Contains(auth, "") {
		t.Errorf("headers: %v", auth)
	}
	// After a logout, the running session sends the key no more.
	if err := store.Logout(rs); err != nil {
		t.Fatal(err)
	}
	if out, err := s.call(t, "old__echo", nil); err == nil || !strings.Contains(out, "run ctxtra mcp login old") {
		t.Errorf("call after logout: %q %v", out, err)
	}
}

// Nothing goes to another origin: not after a redirect, not to an SSE
// message endpoint the server names.
func TestRemoteOtherOrigins(t *testing.T) {
	creds.MockKeychain(nil)
	var hits []string
	var mu sync.Mutex
	other := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		hits = append(hits, r.Method+" "+r.URL.Path+" "+r.Header.Get("X-Api-Key"))
		mu.Unlock()
		w.WriteHeader(http.StatusTeapot)
	}))
	t.Cleanup(other.Close)
	redirect := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, other.URL+"/mcp", http.StatusTemporaryRedirect)
	}))
	t.Cleanup(redirect.Close)
	sse := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			// Not a streamable HTTP server: the proxy tries SSE.
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprintf(w, "event: endpoint\ndata: %s/messages\n\n", other.URL)
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	t.Cleanup(sse.Close)
	for _, srv := range []*httptest.Server{redirect, sse} {
		trustCert(t, srv)
		store := &creds.Store{Dir: t.TempDir()}
		store.EnsureKey()
		rs := upstream.Server{ID: "r", Name: "r", URL: srv.URL + "/mcp", SecretHeaders: []string{"X-Api-Key"}}
		store.SetSecrets(rs, nil, map[string]string{"X-Api-Key": "k"})
		s := startWith(t, t.TempDir(), 5*time.Second, func(o *Options) { o.Tokens, o.Secrets = store, store }, rs)
		if got := s.tools(t); len(got) != 0 {
			t.Errorf("tools %v", toolNames(got))
		}
		refused := false
		for _, rec := range s.records() {
			refused = refused || rec.Event == "connect" && rec.Error == "refused by ctxtra's network rules"
		}
		// The SDK wraps the SSE endpoint's error as text: the requests
		// that reached the other origin, none, show the refusal there.
		if !refused && srv == redirect {
			t.Errorf("%s: log: %+v", srv.URL, s.records())
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if len(hits) > 0 {
		t.Errorf("requests reached another origin: %v", hits)
	}
}

// A private server is reached only when it is on a private address
// itself: loopback stands in here, as the same rule.
func TestRemoteLoopbackOnlyForLoopback(t *testing.T) {
	plain := httptest.NewServer(streamable())
	t.Cleanup(plain.Close)
	rs := upstream.Server{ID: "p", Name: "p", URL: plain.URL + "/mcp"}
	s := startWith(t, t.TempDir(), 5*time.Second, nil, rs)
	if got := toolNames(s.tools(t)); !slices.Equal(got, []string{"p__echo"}) {
		t.Errorf("a plain HTTP server on loopback: %v %+v", got, s.records())
	}
}

// A server whose answer is too large is left out, and the proxy keeps
// serving the others.
func TestRemoteHugeAnswer(t *testing.T) {
	// The most one request wrote: the proxy may try the server twice
	// (streamable HTTP, then SSE).
	var written atomic.Int64
	huge := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		chunk := []byte(strings.Repeat(" ", 1<<20))
		var n int64
		defer func() {
			for cur := written.Load(); n > cur && !written.CompareAndSwap(cur, n); cur = written.Load() {
			}
		}()
		for range 64 {
			m, err := w.Write(chunk)
			n += int64(m)
			if err != nil {
				return
			}
			w.(http.Flusher).Flush()
		}
	}))
	t.Cleanup(huge.Close)
	// Error answers too, whatever type they claim.
	hugeError := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/403" {
			w.WriteHeader(http.StatusForbidden)
		} else {
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusInternalServerError)
		}
		chunk := []byte(strings.Repeat(" ", 1<<20))
		for range 20 {
			if _, err := w.Write(chunk); err != nil {
				return
			}
		}
	}))
	t.Cleanup(hugeError.Close)
	good := httptest.NewServer(streamable())
	t.Cleanup(good.Close)
	s := startWith(t, t.TempDir(), 5*time.Second, nil,
		upstream.Server{ID: "huge", Name: "huge", URL: huge.URL + "/mcp"},
		upstream.Server{ID: "forbidden", Name: "forbidden", URL: hugeError.URL + "/403"},
		upstream.Server{ID: "failing", Name: "failing", URL: hugeError.URL + "/500"},
		upstream.Server{ID: "good", Name: "good", URL: good.URL + "/mcp"})
	if got := toolNames(s.tools(t)); !slices.Equal(got, []string{"good__echo"}) {
		t.Errorf("tools %v", got)
	}
	// The proxy stopped reading at the limit: the server could write
	// little more than that, the sockets' buffers, before the connection
	// closed. Without the limit it writes all 64 MiB.
	if w := written.Load(); w > maxAnswer+8<<20 {
		t.Errorf("the server wrote %d bytes", w)
	}
	var b strings.Builder
	l := &limitedBody{rc: io.NopCloser(strings.NewReader(strings.Repeat("x", maxAnswer+10))), left: maxAnswer}
	if n, err := io.Copy(&b, l); err == nil || n != maxAnswer {
		t.Errorf("limited body: %d %v", n, err)
	}
}
