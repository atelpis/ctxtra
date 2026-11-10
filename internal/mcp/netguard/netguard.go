// Package netguard is the HTTP client for remote MCP servers and their
// authorization servers. It checks every request, not only the configured
// URL: HTTPS only (plain HTTP only on loopback, for a server on
// loopback), no redirect to another origin, requests only to the origins
// it was made for, and, when it connects, no link-local address (such as
// the cloud metadata address) and private addresses only for a server
// that is itself on one. It honors the proxy and certificate variables.
package netguard

import (
	"context"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"time"

	"golang.org/x/net/http/httpproxy"
)

// Policy is what one remote server may reach.
type Policy struct {
	// Host is the configured server's host. Through a proxy, it may be a
	// name that only the proxy resolves; other hosts must resolve here.
	Host string
	// Private allows private addresses: RFC 1918, CGNAT and ULA.
	Private bool
	// Loopback allows loopback addresses and plain HTTP to them.
	Loopback bool
}

// ErrRefused is a request or connection that the rules refuse.
var ErrRefused = errors.New("refused by ctxtra's network rules")

// For is the policy of the server at serverURL: private and loopback
// addresses only when the server itself resolves to one.
func For(ctx context.Context, serverURL string) (Policy, error) {
	u, err := url.Parse(serverURL)
	if err != nil || u.Hostname() == "" {
		return Policy{}, fmt.Errorf("%w: not a URL", ErrRefused)
	}
	addrs, err := lookup(ctx, u.Hostname())
	if err != nil {
		if proxied, perr := proxyFunc()(u); perr == nil && proxied != nil {
			// Only the proxy resolves it: a public server, as far as this
			// machine can tell.
			return Policy{Host: strings.ToLower(u.Hostname())}, nil
		}
		return Policy{}, fmt.Errorf("can't resolve %s: %w", u.Hostname(), err)
	}
	p := Policy{Host: strings.ToLower(u.Hostname())}
	for _, a := range addrs {
		switch kind(a) {
		case loopbackAddr:
			p.Loopback = true
		case privateAddr:
			p.Private = true
		}
	}
	return p, nil
}

func lookup(ctx context.Context, host string) ([]netip.Addr, error) {
	if a, err := netip.ParseAddr(host); err == nil {
		return []netip.Addr{a.Unmap()}, nil
	}
	if host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return []netip.Addr{netip.MustParseAddr("127.0.0.1")}, nil
	}
	ips, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
	for i := range ips {
		ips[i] = ips[i].Unmap()
	}
	return ips, err
}

type addrKind int

const (
	publicAddr addrKind = iota
	privateAddr
	loopbackAddr
	refusedAddr
)

var cgnat = netip.MustParsePrefix("100.64.0.0/10")

func kind(a netip.Addr) addrKind {
	a = a.Unmap()
	switch {
	case a.IsLoopback():
		return loopbackAddr
	case a.IsLinkLocalUnicast(), a.IsLinkLocalMulticast(), a.IsUnspecified(), a.IsMulticast(), a.IsInterfaceLocalMulticast():
		return refusedAddr
	case a.IsPrivate(), cgnat.Contains(a):
		return privateAddr
	}
	return publicAddr
}

// allows reports whether p may connect to a.
func (p Policy) allows(a netip.Addr) bool {
	switch kind(a) {
	case publicAddr:
		return true
	case privateAddr:
		return p.Private
	case loopbackAddr:
		return p.Loopback
	}
	return false
}

// Client is an HTTP client under p that sends requests only to origins
// (scheme://host[:port]); none allows any origin, for discovery that
// follows the metadata. Redirects to another origin are never followed.
func (p Policy) Client(origins ...string) *http.Client {
	proxy := proxyFunc()
	base := &http.Transport{
		Proxy:                 func(r *http.Request) (*url.URL, error) { return proxy(r.URL) },
		DialContext:           p.dialer(proxy),
		TLSClientConfig:       tlsConfig(),
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          10,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: time.Second,
	}
	return &http.Client{
		Transport: &guard{p: p, origins: canonical(origins), base: base, proxy: proxy},
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return fmt.Errorf("%w: too many redirects", ErrRefused)
			}
			if Origin(req.URL) != Origin(via[0].URL) {
				return fmt.Errorf("%w: a redirect to another origin (%s)", ErrRefused, Origin(req.URL))
			}
			return nil
		},
	}
}

// Origin is u's scheme://host[:port], lower case, without a default port.
func Origin(u *url.URL) string {
	host := strings.ToLower(u.Hostname())
	port := u.Port()
	if u.Scheme == "https" && port == "443" || u.Scheme == "http" && port == "80" {
		port = ""
	}
	if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	if port != "" {
		host += ":" + port
	}
	return strings.ToLower(u.Scheme) + "://" + host
}

func canonical(origins []string) []string {
	var out []string
	for _, o := range origins {
		if u, err := url.Parse(o); err == nil {
			out = append(out, Origin(u))
		}
	}
	return out
}

// guard checks each request before it goes out.
type guard struct {
	p       Policy
	origins []string
	base    *http.Transport
	proxy   func(*url.URL) (*url.URL, error)
}

func (g *guard) RoundTrip(req *http.Request) (*http.Response, error) {
	if err := g.check(req); err != nil {
		if req.Body != nil {
			req.Body.Close()
		}
		return nil, err
	}
	return g.base.RoundTrip(req)
}

func (g *guard) check(req *http.Request) error {
	u := req.URL
	if len(g.origins) > 0 && !slices.Contains(g.origins, Origin(u)) {
		return fmt.Errorf("%w: a request to another origin (%s)", ErrRefused, Origin(u))
	}
	switch u.Scheme {
	case "https":
	case "http":
		if !g.p.Loopback || !isLoopbackHost(u.Hostname()) {
			return fmt.Errorf("%w: %s doesn't use HTTPS", ErrRefused, Origin(u))
		}
	default:
		return fmt.Errorf("%w: %s", ErrRefused, Origin(u))
	}
	// Through a proxy, ctxtra connects only to the proxy: the target's
	// addresses are checked here, as this host resolves them. A host that
	// only the proxy can resolve is allowed only when it is the configured
	// server's own; the proxy may still resolve a name differently.
	if proxied, err := g.proxy(u); err == nil && proxied != nil {
		addrs, err := lookup(req.Context(), u.Hostname())
		if err != nil {
			if strings.EqualFold(u.Hostname(), g.p.Host) {
				return nil
			}
			return fmt.Errorf("%w: %s doesn't resolve here, through a proxy", ErrRefused, u.Hostname())
		}
		for _, a := range addrs {
			if !g.p.allows(a) {
				return fmt.Errorf("%w: %s is at %s", ErrRefused, u.Hostname(), a)
			}
		}
	}
	return nil
}

func isLoopbackHost(host string) bool {
	addrs, err := lookup(context.Background(), host)
	if err != nil || len(addrs) == 0 {
		return false
	}
	for _, a := range addrs {
		if !a.IsLoopback() {
			return false
		}
	}
	return true
}

// dialer connects only to addresses p allows, checked on the address it
// connects to, so a DNS change between a check and the connection can't
// get around it. The proxy's own address is not checked: it is the
// user's.
func (p Policy) dialer(proxy func(*url.URL) (*url.URL, error)) func(ctx context.Context, network, addr string) (net.Conn, error) {
	checked := &net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second,
		Control: func(_, address string, _ syscall.RawConn) error {
			ap, err := netip.ParseAddrPort(address)
			if err != nil {
				return fmt.Errorf("%w: %s", ErrRefused, address)
			}
			if !p.allows(ap.Addr()) {
				return fmt.Errorf("%w: %s", ErrRefused, ap.Addr())
			}
			return nil
		}}
	plain := &net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second}
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		if isProxy(proxy, addr) {
			return plain.DialContext(ctx, network, addr)
		}
		return checked.DialContext(ctx, network, addr)
	}
}

// isProxy reports whether addr is a configured proxy's address.
func isProxy(proxy func(*url.URL) (*url.URL, error), addr string) bool {
	for _, scheme := range []string{"https", "http"} {
		// A host that no NO_PROXY entry matches, to learn the proxy.
		u, err := proxy(&url.URL{Scheme: scheme, Host: "ctxtra-proxy-probe.invalid"})
		if err != nil || u == nil {
			continue
		}
		port := u.Port()
		if port == "" {
			port = map[string]string{"http": "80", "https": "443", "socks5": "1080", "socks5h": "1080"}[u.Scheme]
		}
		if strings.EqualFold(net.JoinHostPort(u.Hostname(), port), addr) {
			return true
		}
	}
	return false
}

// proxyFunc is the proxy for a URL from HTTPS_PROXY, HTTP_PROXY, NO_PROXY
// and ALL_PROXY (also lower case). ALL_PROXY is used for a scheme without
// its own variable.
func proxyFunc() func(*url.URL) (*url.URL, error) {
	env := func(name string) string {
		if v := os.Getenv(name); v != "" {
			return v
		}
		return os.Getenv(strings.ToLower(name))
	}
	all := env("ALL_PROXY")
	cfg := httpproxy.Config{HTTPSProxy: env("HTTPS_PROXY"), HTTPProxy: env("HTTP_PROXY"), NoProxy: env("NO_PROXY")}
	if cfg.HTTPSProxy == "" {
		cfg.HTTPSProxy = all
	}
	if cfg.HTTPProxy == "" {
		cfg.HTTPProxy = all
	}
	return cfg.ProxyFunc()
}

// roots are the system's certificates and those of SSL_CERT_FILE and
// SSL_CERT_DIR, which Go doesn't read on macOS by itself.
func roots() *x509.CertPool {
	file, dir := os.Getenv("SSL_CERT_FILE"), os.Getenv("SSL_CERT_DIR")
	if file == "" && dir == "" {
		return nil
	}
	pool, err := x509.SystemCertPool()
	if err != nil {
		pool = x509.NewCertPool()
	}
	var files []string
	if file != "" {
		files = append(files, file)
	}
	for _, d := range filepath.SplitList(dir) {
		entries, _ := os.ReadDir(d)
		for _, e := range entries {
			files = append(files, filepath.Join(d, e.Name()))
		}
	}
	for _, f := range files {
		if data, err := os.ReadFile(f); err == nil {
			pool.AppendCertsFromPEM(data)
		}
	}
	return pool
}
