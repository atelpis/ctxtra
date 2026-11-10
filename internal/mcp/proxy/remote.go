package proxy

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/ctxtra/ctxtra/internal/mcp/creds"
	"github.com/ctxtra/ctxtra/internal/mcp/netguard"
	"github.com/ctxtra/ctxtra/internal/mcp/oauth"
	"github.com/ctxtra/ctxtra/internal/mcp/upstream"
)

// Tokens are where remote servers' OAuth tokens come from: the credential
// store. It refreshes them; nothing here ever starts a sign-in.
type Tokens interface {
	// Token is the access token for s; a creds.LoginError with Never set
	// when s has no sign-in.
	Token(ctx context.Context, s upstream.Server) (string, error)
	// Rejected refreshes after s rejected token with 401.
	Rejected(ctx context.Context, s upstream.Server, token string) (string, error)
	// NeedsLogin and StepUp record why s needs sign-in, and are the error
	// to return.
	NeedsLogin(s upstream.Server, reason string) error
	StepUp(s upstream.Server, scopes []string) error
}

// dialRemote connects to the remote server s: streamable HTTP, and the
// older SSE transport when that fails. Every request goes through ctxtra's
// network rules, and to the server's origin only.
func (d Dialer) dialRemote(ctx context.Context, s upstream.Server, c *mcp.Client) (*Upstream, error) {
	if len(s.SecretHeaders) > 0 && d.Secrets == nil {
		return nil, ErrSecrets
	}
	policy, err := netguard.For(ctx, s.URL)
	if err != nil {
		return nil, err
	}
	u, err := url.Parse(s.URL)
	if err != nil {
		return nil, err
	}
	hc := policy.Client(netguard.Origin(u))
	hc.Transport = &authTransport{base: hc.Transport, srv: s, tokens: d.Tokens, secrets: d.Secrets}
	cs, err := c.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: s.URL, HTTPClient: hc}, nil)
	if err != nil {
		var lerr *creds.LoginError
		if errors.As(err, &lerr) || errors.Is(err, netguard.ErrRefused) || ctx.Err() != nil {
			return nil, err
		}
		sse, serr := c.Connect(ctx, &mcp.SSEClientTransport{Endpoint: s.URL, HTTPClient: hc}, nil)
		if serr != nil {
			// The SSE server's own reason, when it is one ctxtra can act on.
			if errors.As(serr, &lerr) || errors.Is(serr, netguard.ErrRefused) {
				return nil, serr
			}
			return nil, err
		}
		cs = sse
	}
	return &Upstream{Server: s, Session: cs}, nil
}

// authTransport adds a remote server's headers and its stored access
// token to each request. A 401 gets one refresh and one retry; a 401
// without a sign-in, a second 401, or a 403 insufficient_scope fails the
// request with the hint to run ctxtra mcp login. It never signs in.
type authTransport struct {
	base    http.RoundTripper
	srv     upstream.Server
	tokens  Tokens
	secrets Secrets
}

func (t *authTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	// Secret headers are read for each request, so a logout or a new
	// value applies to running sessions too.
	headers := map[string]string{}
	for k, v := range t.srv.Headers {
		headers[k] = v
	}
	if len(t.srv.SecretHeaders) > 0 {
		_, secret, err := t.secrets.Secrets(t.srv)
		if err != nil {
			closeBody(req)
			return nil, secretsError{err}
		}
		for k, v := range secret {
			headers[k] = v
		}
	}
	token := ""
	if t.tokens != nil {
		tok, err := t.tokens.Token(req.Context(), t.srv)
		var lerr *creds.LoginError
		switch {
		case errors.As(err, &lerr) && lerr.Never:
		case err != nil:
			closeBody(req)
			return nil, err
		default:
			token = tok
		}
	}
	body, err := bodyOf(req)
	if err != nil {
		return nil, err
	}
	resp, err := t.send(req, body, headers, token)
	if err != nil || resp.StatusCode != http.StatusUnauthorized && resp.StatusCode != http.StatusForbidden {
		return resp, err
	}
	if resp.StatusCode == http.StatusForbidden {
		return t.forbidden(resp)
	}
	drain(resp)
	if t.tokens == nil {
		return nil, &creds.LoginError{Name: t.srv.Name, Reason: "the server wants a sign-in"}
	}
	if token == "" {
		return nil, t.tokens.NeedsLogin(t.srv, "not signed in")
	}
	token, err = t.tokens.Rejected(req.Context(), t.srv, token)
	if err != nil {
		return nil, err
	}
	resp, err = t.send(req, body, headers, token)
	switch {
	case err != nil:
		return nil, err
	case resp.StatusCode == http.StatusForbidden:
		return t.forbidden(resp)
	case resp.StatusCode != http.StatusUnauthorized:
		return resp, nil
	}
	drain(resp)
	return nil, t.tokens.NeedsLogin(t.srv, "the server rejected the sign-in")
}

// forbidden handles a 403: insufficient_scope records the scopes it asks
// for and fails with the login hint; any other 403 is the server's answer.
func (t *authTransport) forbidden(resp *http.Response) (*http.Response, error) {
	if t.tokens == nil || !insufficientScope(resp) {
		return resp, nil
	}
	var scopes []string
	if ch := oauth.ParseChallenge(resp.Header.Values("WWW-Authenticate")); ch != nil {
		scopes = ch.Scopes
	}
	drain(resp)
	return nil, t.tokens.StepUp(t.srv, scopes)
}

func (t *authTransport) send(req *http.Request, body []byte, headers map[string]string, token string) (*http.Response, error) {
	r := req.Clone(req.Context())
	if body != nil {
		r.Body = io.NopCloser(bytes.NewReader(body))
		r.GetBody = func() (io.ReadCloser, error) { return io.NopCloser(bytes.NewReader(body)), nil }
	}
	for k, v := range headers {
		r.Header.Set(k, v)
	}
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	return t.base.RoundTrip(r)
}

// bodyOf reads the request's body, so a retry can send it again.
func bodyOf(req *http.Request) ([]byte, error) {
	if req.Body == nil || req.Body == http.NoBody {
		return nil, nil
	}
	defer req.Body.Close()
	return io.ReadAll(req.Body)
}

func closeBody(req *http.Request) {
	if req.Body != nil {
		req.Body.Close()
	}
}

func drain(resp *http.Response) {
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<16))
	resp.Body.Close()
}

func insufficientScope(resp *http.Response) bool {
	for _, h := range resp.Header.Values("WWW-Authenticate") {
		if bytes.Contains([]byte(h), []byte("insufficient_scope")) {
			return true
		}
	}
	return false
}
