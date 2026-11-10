// Package oauth signs in to remote MCP servers as the MCP authorization
// spec (2026-07-28) describes for clients: discovery with an issuer
// check, client registration, PKCE with S256, the resource parameter and
// the iss check. Only the commands the user runs in a terminal sign in;
// the proxy only uses the stored tokens.
package oauth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/oauthex"

	"github.com/ctxtra/ctxtra/internal/mcp/creds"
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
	asm, err := serverMetadata(ctx, c, issuer)
	if err != nil {
		return nil, err
	}
	if len(asm.CodeChallengeMethodsSupported) == 0 {
		return nil, fmt.Errorf("the authorization server %s doesn't offer PKCE", issuer)
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
		var prm oauthex.ProtectedResourceMetadata
		found, err := getJSON(ctx, c, mu, &prm)
		if err != nil {
			return nil, fmt.Errorf("the server's protected resource metadata: %w", err)
		}
		if !found {
			continue
		}
		// RFC 9728 3.3: the metadata is for this resource.
		if creds.CanonicalURL(prm.Resource) != creds.CanonicalURL(resource) {
			return nil, fmt.Errorf("the server's protected resource metadata is for another resource")
		}
		return &prm, nil
	}
	return nil, nil
}

// serverMetadata is the authorization server metadata of issuer (RFC
// 8414, and OpenID Connect discovery), which must name that issuer.
func serverMetadata(ctx context.Context, c *http.Client, issuer string) (*oauthex.AuthServerMeta, error) {
	u, err := url.Parse(issuer)
	if err != nil || u.Host == "" {
		return nil, fmt.Errorf("the authorization server %q is not a URL", issuer)
	}
	if u.Scheme != "https" && !(u.Scheme == "http" && loopback(u.Hostname())) {
		return nil, fmt.Errorf("the authorization server %s doesn't use HTTPS", netguard.Origin(u))
	}
	origin, p := netguard.Origin(u), strings.Trim(u.Path, "/")
	var urls []string
	if p == "" {
		urls = []string{origin + "/.well-known/oauth-authorization-server", origin + "/.well-known/openid-configuration"}
	} else {
		urls = []string{origin + "/.well-known/oauth-authorization-server/" + p,
			origin + "/.well-known/openid-configuration/" + p, origin + "/" + p + "/.well-known/openid-configuration"}
	}
	for _, mu := range urls {
		var asm oauthex.AuthServerMeta
		found, err := getJSON(ctx, c, mu, &asm)
		if err != nil {
			return nil, fmt.Errorf("the authorization server %s: %w", origin, err)
		}
		if !found {
			continue
		}
		if strings.TrimSuffix(asm.Issuer, "/") != strings.TrimSuffix(issuer, "/") {
			return nil, fmt.Errorf("the authorization server's metadata names another issuer than %s", issuer)
		}
		return &asm, nil
	}
	return nil, fmt.Errorf("found no authorization server metadata for %s", issuer)
}

// getJSON reads a JSON document at u into v; false when there is none (a
// 4xx answer). Errors don't quote the answer.
func getJSON(ctx context.Context, c *http.Client, u string, v any) (bool, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return false, err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := c.Do(req)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode >= 400 && resp.StatusCode < 500:
		return false, nil
	case resp.StatusCode != http.StatusOK:
		return false, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(v); err != nil {
		return false, errors.New("the answer isn't a JSON document")
	}
	return true, nil
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
