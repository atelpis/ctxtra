package oauth

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/oauthex"

	"github.com/ctxtra/ctxtra/internal/mcp/creds"
	"github.com/ctxtra/ctxtra/internal/mcp/upstream"
)

// ClientMetadataURL is the URL of ctxtra's client ID metadata document.
// Empty until the domain exists and real providers accept it: until
// then, sign-in uses dynamic registration.
const ClientMetadataURL = ""

// SignInTimeout is how long the redirect listener waits.
const SignInTimeout = 5 * time.Minute

// SignIn is one sign-in to Server.
type SignIn struct {
	Server upstream.Server
	Store  *creds.Store
	// HTTP sends discovery, registration and token requests.
	HTTP *http.Client
	// Out gets the messages for the user; In the pasted redirect URL.
	Out io.Writer
	In  io.Reader
	// OpenBrowser opens the authorization URL; with NoBrowser, the URL
	// is printed and the redirect URL can be pasted.
	OpenBrowser func(string) error
	NoBrowser   bool
	// CallbackPort is the listener's port; 0 picks one.
	CallbackPort int
	// ClientSecret is the secret of Server.ClientID, if it has one.
	ClientSecret string
	// Scopes are asked for besides the server's own: an earlier sign-in's,
	// and those a 403 asked for.
	Scopes  []string
	Timeout time.Duration
}

// Result is what a sign-in got.
type Result struct {
	Issuer string
	// Account is the account the provider's answer named, "" when it
	// named none.
	Account string
	// NoRefresh is set when the provider issued no refresh token.
	NoRefresh bool
	Record    *creds.Record
}

// ErrNeedsClientID is a server that supports neither client ID metadata
// documents nor dynamic registration.
var ErrNeedsClientID = errors.New("the server's authorization server accepts only registered apps: create an OAuth app there and pass --client-id")

// Run signs in: discovery, registration, the browser, the redirect, the
// token request, and the save, which fails when another sign-in or a
// removal finished meanwhile. ch is the server's 401 challenge.
func (f *SignIn) Run(ctx context.Context, ch *Challenge) (*Result, error) {
	gen, err := f.Store.Generation(f.Server)
	if err != nil {
		return nil, err
	}
	resource := creds.CanonicalURL(f.Server.URL)
	m, err := Discover(ctx, f.HTTP, resource, ch)
	if err != nil {
		return nil, err
	}
	scopes := Scopes(f.Server.Scopes, ch, m, f.Scopes)
	port := f.CallbackPort
	ln, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
	if err != nil {
		return nil, fmt.Errorf("can't listen for the sign-in's redirect on 127.0.0.1:%d: %w", port, err)
	}
	defer ln.Close()
	redirect := fmt.Sprintf("http://127.0.0.1:%d/callback", ln.Addr().(*net.TCPAddr).Port)
	client, err := f.client(ctx, m, redirect)
	if err != nil {
		return nil, err
	}
	issuer := m.Server.Issuer
	if others := f.Store.ServersOf(issuer, f.Server); len(others) > 0 {
		fmt.Fprintf(f.Out, "You are already signed in to %s as %s. The provider may use the account of your browser's\n"+
			"active session; to sign in with another one, open the link in a private window.\n", issuer, strings.Join(others, ", "))
	}

	state, verifier := random(), random()
	sum := sha256.Sum256([]byte(verifier))
	q := url.Values{
		"response_type":         {"code"},
		"client_id":             {client.ID},
		"redirect_uri":          {redirect},
		"state":                 {state},
		"code_challenge":        {base64.RawURLEncoding.EncodeToString(sum[:])},
		"code_challenge_method": {"S256"},
		"resource":              {resource},
	}
	if len(scopes) > 0 {
		q.Set("scope", strings.Join(scopes, " "))
	}
	authURL := m.Server.AuthorizationEndpoint
	if strings.Contains(authURL, "?") {
		authURL += "&" + q.Encode()
	} else {
		authURL += "?" + q.Encode()
	}

	timeout := f.Timeout
	if timeout == 0 {
		timeout = SignInTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	got := make(chan url.Values, 2)
	srv := &http.Server{Handler: callback(state, got), ReadHeaderTimeout: 10 * time.Second}
	go func() { _ = srv.Serve(ln) }()
	defer srv.Close()

	if f.NoBrowser || f.OpenBrowser == nil {
		fmt.Fprintf(f.Out, "Open this link to sign in to %s:\n\n  %s\n\n", f.Server.Name, authURL)
		fmt.Fprintln(f.Out, "If your browser can't reach this machine (for example over SSH), paste the address it ends at here:")
		go f.readPasted(got)
	} else {
		fmt.Fprintf(f.Out, "Opening your browser to sign in to %s. If it doesn't open, use this link:\n\n  %s\n\n", f.Server.Name, authURL)
		if err := f.OpenBrowser(authURL); err != nil {
			fmt.Fprintf(f.Out, "couldn't open the browser: %v\n", err)
		}
	}

	var params url.Values
	select {
	case params = <-got:
	case <-ctx.Done():
		return nil, fmt.Errorf("no sign-in came back within %s", timeout)
	}
	if params.Get("state") != state {
		return nil, errors.New("the pasted address is from another sign-in; nothing was saved")
	}
	if e := params.Get("error"); e != "" {
		return nil, fmt.Errorf("the provider refused the sign-in: %s", safeCode(e))
	}
	iss := params.Get("iss")
	if iss != "" && iss != issuer || iss == "" && m.Server.AuthorizationResponseIssParameterSupported {
		return nil, errors.New("the sign-in's answer is from another authorization server (iss); nothing was saved")
	}
	code := params.Get("code")
	if code == "" {
		return nil, errors.New("the sign-in's answer has no code")
	}
	tok, extra, err := f.Store.RequestToken(ctx, client, url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"redirect_uri":  {redirect},
		"code_verifier": {verifier},
		"resource":      {resource},
	})
	if err != nil {
		return nil, err
	}
	if len(tok.Scopes) == 0 {
		tok.Scopes = scopes
	}
	r, err := f.Store.SaveSignIn(f.Server, gen, issuer, client, tok)
	if err != nil {
		return nil, err
	}
	return &Result{Issuer: issuer, Account: account(extra), NoRefresh: tok.Refresh == "", Record: r}, nil
}

// client is the OAuth client to sign in with, in the spec's order: the
// configured client ID, ctxtra's client ID metadata document, then a
// dynamic registration (the stored one when it has this redirect).
func (f *SignIn) client(ctx context.Context, m *Metadata, redirect string) (creds.Client, error) {
	tokenURL := m.Server.TokenEndpoint
	if f.Server.ClientID != "" {
		c := creds.Client{ID: f.Server.ClientID, Secret: f.ClientSecret, AuthMethod: "none", TokenURL: tokenURL}
		if c.Secret != "" {
			c.AuthMethod = "client_secret_post"
			if len(m.Server.TokenEndpointAuthMethodsSupported) == 0 || slices.Contains(m.Server.TokenEndpointAuthMethodsSupported, "client_secret_basic") {
				c.AuthMethod = "client_secret_basic"
			}
		}
		return c, nil
	}
	if ClientMetadataURL != "" && m.Server.ClientIDMetadataDocumentSupported {
		return creds.Client{ID: ClientMetadataURL, AuthMethod: "none", TokenURL: tokenURL}, nil
	}
	if r, err := f.Store.Load(f.Server); err == nil && r != nil && r.Matches(f.Server) &&
		r.Client.Registered && r.Client.Redirect == redirect && r.Client.TokenURL == tokenURL {
		return r.Client, nil
	}
	if m.Server.RegistrationEndpoint == "" {
		return creds.Client{}, ErrNeedsClientID
	}
	reg, err := oauthex.RegisterClient(ctx, m.Server.RegistrationEndpoint, &oauthex.ClientRegistrationMetadata{
		RedirectURIs:            []string{redirect},
		TokenEndpointAuthMethod: "none",
		GrantTypes:              []string{"authorization_code", "refresh_token"},
		ResponseTypes:           []string{"code"},
		ClientName:              "ctxtra",
	}, f.HTTP)
	if err != nil {
		return creds.Client{}, fmt.Errorf("registering ctxtra with the authorization server failed: %w", err)
	}
	c := creds.Client{ID: reg.ClientID, Secret: reg.ClientSecret, AuthMethod: reg.TokenEndpointAuthMethod,
		TokenURL: tokenURL, Redirect: redirect, Registered: true}
	if c.AuthMethod == "" {
		c.AuthMethod = "none"
		if c.Secret != "" {
			c.AuthMethod = "client_secret_basic"
		}
	}
	return c, nil
}

// callback is the redirect listener's handler. A request with another
// state is refused and the listener keeps waiting; the first right one
// ends the sign-in.
func callback(state string, got chan<- url.Values) http.Handler {
	var mu sync.Mutex
	done := false
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/callback" {
			http.NotFound(w, r)
			return
		}
		q := r.URL.Query()
		mu.Lock()
		first := q.Get("state") == state && !done
		done = done || first
		mu.Unlock()
		if !first {
			http.Error(w, "This isn't the sign-in ctxtra is waiting for.", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		if q.Get("error") != "" {
			io.WriteString(w, "The sign-in was refused. You can close this tab.\n")
		} else {
			io.WriteString(w, "Signed in to ctxtra. You can close this tab.\n")
		}
		select {
		case got <- q:
		default:
		}
	})
}

// readPasted reads the address the browser ended at.
func (f *SignIn) readPasted(got chan<- url.Values) {
	if f.In == nil {
		return
	}
	line, err := bufio.NewReader(f.In).ReadString('\n')
	if err != nil && line == "" {
		return
	}
	u, err := url.Parse(strings.TrimSpace(line))
	if err != nil {
		return
	}
	select {
	case got <- u.Query():
	default:
	}
}

func random() string {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

// safeCode is an OAuth error code from a redirect, or "an error" for text
// that isn't one: the provider controls it.
func safeCode(s string) string {
	for _, r := range s {
		if (r < 'a' || r > 'z') && r != '_' {
			return "an error"
		}
	}
	if len(s) > 40 {
		return "an error"
	}
	return s
}

// account is the account a token answer names: the email or name in its
// ID token, unverified (it's only shown), or "" without one.
func account(extra map[string]json.RawMessage) string {
	var idToken string
	if json.Unmarshal(extra["id_token"], &idToken) != nil {
		return ""
	}
	parts := strings.Split(idToken, ".")
	if len(parts) != 3 {
		return ""
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return ""
	}
	var claims struct {
		Email             string `json:"email"`
		PreferredUsername string `json:"preferred_username"`
		Name              string `json:"name"`
	}
	if json.Unmarshal(payload, &claims) != nil {
		return ""
	}
	for _, v := range []string{claims.Email, claims.PreferredUsername, claims.Name} {
		if v != "" {
			return v
		}
	}
	return ""
}
