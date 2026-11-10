// Package oauth signs in to remote MCP servers as the MCP authorization
// spec (2026-07-28) describes for clients: discovery with an issuer
// check, client registration, PKCE with S256, the resource parameter and
// the iss check. Only the commands the user runs in a terminal sign in;
// the proxy only uses the stored tokens.
package oauth

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/modelcontextprotocol/go-sdk/oauthex"

	"github.com/ctxtra/ctxtra/internal/mcp/netguard"
)

// Challenge is what a server's 401 said: it uses OAuth, where its
// protected resource metadata is, and which scopes it asks for.
type Challenge struct {
	ResourceMetadata string
	Scopes           []string
}

// initialize is an MCP initialize request, to learn whether a server
// needs sign-in.
const initialize = `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"ctxtra","version":"0"}}}`

// Detect sends an initialize without a token: a 401 with a Bearer
// challenge means the server uses OAuth, nil means it doesn't. headers
// are the server's own headers, its secret ones too.
func Detect(ctx context.Context, c *http.Client, serverURL string, headers map[string]string) (*Challenge, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, serverURL, strings.NewReader(initialize))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := c.Do(req)
	if err != nil {
		return nil, err
	}
	resp.Body.Close()
	if resp.StatusCode == http.StatusMethodNotAllowed || resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusBadRequest {
		// An older SSE server: it is opened with GET.
		if resp, err = sseProbe(ctx, c, serverURL, headers); err != nil {
			return nil, err
		}
	}
	if resp.StatusCode != http.StatusUnauthorized {
		return nil, nil
	}
	// A 401 without a Bearer challenge is about the server's own headers,
	// such as an API key, not OAuth.
	return ParseChallenge(resp.Header.Values("WWW-Authenticate")), nil
}

// sseProbe opens the SSE stream at serverURL without a token, and closes
// it at once: only the status and headers matter.
func sseProbe(ctx context.Context, c *http.Client, serverURL string, headers map[string]string) (*http.Response, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, serverURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "text/event-stream")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := c.Do(req)
	if err != nil {
		return nil, err
	}
	resp.Body.Close()
	return resp, nil
}

// ParseChallenge reads the Bearer challenge of a 401 or 403 answer: nil
// without one.
func ParseChallenge(headers []string) *Challenge {
	challenges, err := oauthex.ParseWWWAuthenticate(headers)
	if err != nil {
		return nil
	}
	for _, c := range challenges {
		if c.Scheme == "bearer" {
			return &Challenge{ResourceMetadata: c.Params["resource_metadata"], Scopes: strings.Fields(c.Params["scope"])}
		}
	}
	return nil
}

// Metadata is what discovery found for a server.
type Metadata struct {
	// Resource is the canonical server URL, sent as the resource
	// parameter.
	Resource string
	// ResourceScopes are the protected resource metadata's
	// scopes_supported.
	ResourceScopes []string
	Server         *oauthex.AuthServerMeta
}

// Discover finds the authorization server of the server at resource:
// from its protected resource metadata (where the challenge says, else
// at the well-known places), else at the server's origin, as servers
// without that metadata (Atlassian) need. The authorization server's
// metadata must name the issuer it was found for, offer PKCE with S256,
// and use HTTPS for every endpoint (loopback endpoints only for a
// loopback server).
func Discover(ctx context.Context, c *http.Client, resource string, ch *Challenge) (*Metadata, error) {
	u, err := url.Parse(resource)
	if err != nil {
		return nil, err
	}
	m := &Metadata{Resource: resource}
	issuer := netguard.Origin(u)
	prm, err := resourceMetadata(ctx, c, resource, ch)
	if err != nil {
		return nil, err
	}
	if prm != nil {
		if len(prm.AuthorizationServers) == 0 {
			return nil, errors.New("the server's protected resource metadata names no authorization server")
		}
		issuer = prm.AuthorizationServers[0]
		m.ResourceScopes = prm.ScopesSupported
	}
	asm, err := auth.GetAuthServerMetadata(ctx, issuer, c)
	if err != nil {
		return nil, fmt.Errorf("the authorization server %s: %w", issuer, err)
	}
	if asm == nil {
		return nil, fmt.Errorf("found no authorization server metadata for %s", issuer)
	}
	if !slices.Contains(asm.CodeChallengeMethodsSupported, "S256") {
		return nil, fmt.Errorf("the authorization server %s doesn't offer PKCE with S256", issuer)
	}
	if err := checkEndpoints(asm, resource); err != nil {
		return nil, err
	}
	m.Server = asm
	return m, nil
}

// resourceMetadata is the server's protected resource metadata (RFC
// 9728), nil when it has none.
func resourceMetadata(ctx context.Context, c *http.Client, resource string, ch *Challenge) (*oauthex.ProtectedResourceMetadata, error) {
	var urls []string
	if ch != nil && ch.ResourceMetadata != "" {
		urls = append(urls, ch.ResourceMetadata)
	}
	u, _ := url.Parse(resource)
	origin := netguard.Origin(u)
	if p := strings.TrimSuffix(u.Path, "/"); p != "" {
		urls = append(urls, origin+"/.well-known/oauth-protected-resource"+p)
	}
	urls = append(urls, origin+"/.well-known/oauth-protected-resource")
	for _, mu := range urls {
		prm, err := oauthex.GetProtectedResourceMetadata(ctx, mu, resource, c)
		if err == nil {
			return prm, nil
		}
		if !notFound(ctx, c, mu) {
			return nil, fmt.Errorf("the server's protected resource metadata: %w", err)
		}
	}
	return nil, nil
}

// notFound reports whether u answers with a 4xx status: no document
// there, so discovery goes on.
func notFound(ctx context.Context, c *http.Client, u string) bool {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return false
	}
	resp, err := c.Do(req)
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode >= 400 && resp.StatusCode < 500
}

// checkEndpoints refuses endpoints without HTTPS, except loopback ones of
// a loopback server.
func checkEndpoints(asm *oauthex.AuthServerMeta, resource string) error {
	su, _ := url.Parse(resource)
	for _, e := range []struct{ name, url string }{
		{"authorization", asm.AuthorizationEndpoint},
		{"token", asm.TokenEndpoint},
		{"registration", asm.RegistrationEndpoint},
	} {
		if e.url == "" {
			if e.name == "registration" {
				continue
			}
			return fmt.Errorf("the authorization server has no %s endpoint", e.name)
		}
		u, err := url.Parse(e.url)
		if err != nil {
			return fmt.Errorf("the %s endpoint is not a URL", e.name)
		}
		if u.Scheme == "https" || u.Scheme == "http" && loopback(u.Hostname()) && loopback(su.Hostname()) {
			continue
		}
		return fmt.Errorf("the %s endpoint %s doesn't use HTTPS", e.name, netguard.Origin(u))
	}
	return nil
}

func loopback(host string) bool {
	return host == "localhost" || host == "127.0.0.1" || host == "::1"
}

// Scopes are the scopes to ask for: the configured ones, else the
// challenge's, else the protected resource metadata's, with extra (those
// of an earlier sign-in and those a 403 asked for), and offline_access
// when the authorization server lists it, so that it issues a refresh
// token.
func Scopes(configured []string, ch *Challenge, m *Metadata, extra ...[]string) []string {
	var out []string
	switch {
	case len(configured) > 0:
		out = slices.Clone(configured)
	case ch != nil && len(ch.Scopes) > 0:
		out = slices.Clone(ch.Scopes)
	default:
		out = slices.Clone(m.ResourceScopes)
	}
	for _, e := range extra {
		out = append(out, e...)
	}
	if slices.Contains(m.Server.ScopesSupported, "offline_access") {
		out = append(out, "offline_access")
	}
	slices.Sort(out)
	return slices.Compact(out)
}
