package netguard

import (
	"context"
	"encoding/pem"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// trust makes the test server's certificate trusted through
// SSL_CERT_FILE, as a user's company certificate would be.
func trust(t *testing.T, srv *httptest.Server) {
	t.Helper()
	f := filepath.Join(t.TempDir(), "ca.pem")
	os.WriteFile(f, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: srv.Certificate().Raw}), 0o600)
	t.Setenv("SSL_CERT_FILE", f)
}

func ok() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { io.WriteString(w, "ok") })
}

func get(c *http.Client, u string) error {
	resp, err := c.Get(u)
	if err != nil {
		return err
	}
	resp.Body.Close()
	return nil
}

func TestFor(t *testing.T) {
	for host, want := range map[string]Policy{
		"127.0.0.1":     {Loopback: true},
		"localhost":     {Loopback: true},
		"10.1.2.3":      {Private: true},
		"100.64.0.1":    {Private: true},
		"[fd00::1]":     {Private: true},
		"93.184.216.34": {},
	} {
		got, err := For(context.Background(), "https://"+host+"/mcp")
		got.Host = ""
		if err != nil || got != want {
			t.Errorf("%s: %+v %v, want %+v", host, got, err, want)
		}
	}
}

func TestHTTPSAndCertificates(t *testing.T) {
	srv := httptest.NewTLSServer(ok())
	defer srv.Close()
	trust(t, srv)
	if err := get(Policy{Loopback: true}.Client(srv.URL), srv.URL); err != nil {
		t.Errorf("a trusted loopback server: %v", err)
	}
	if err := get(Policy{Loopback: true}.Client(), "http://example.com/"); !errors.Is(err, ErrRefused) {
		t.Errorf("plain HTTP to another host: %v", err)
	}
	plain := httptest.NewServer(ok())
	defer plain.Close()
	if err := get(Policy{Loopback: true}.Client(), plain.URL); err != nil {
		t.Errorf("plain HTTP on loopback for a loopback server: %v", err)
	}
	if err := get(Policy{}.Client(), plain.URL); !errors.Is(err, ErrRefused) {
		t.Errorf("plain HTTP on loopback for a public server: %v", err)
	}
}

func TestOrigins(t *testing.T) {
	a := httptest.NewTLSServer(ok())
	defer a.Close()
	b := httptest.NewTLSServer(ok())
	defer b.Close()
	redirect := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, b.URL+"/token", http.StatusTemporaryRedirect)
	}))
	defer redirect.Close()
	trust(t, a)
	c := Policy{Loopback: true}.Client(a.URL)
	if err := get(c, b.URL); !errors.Is(err, ErrRefused) {
		t.Errorf("a request to another origin: %v", err)
	}
	// Each httptest server has its own certificate; skip verification to
	// reach the redirect.
	open := Policy{Loopback: true}.Client()
	open.Transport.(*guard).base.TLSClientConfig.InsecureSkipVerify = true
	if err := get(open, redirect.URL); !errors.Is(err, ErrRefused) {
		t.Errorf("a redirect to another origin: %v", err)
	}
}

// Addresses are checked when connecting, whatever the name resolved to.
func TestDialChecks(t *testing.T) {
	for _, tc := range []struct {
		url string
		p   Policy
	}{
		{"https://169.254.169.254/latest/meta-data", Policy{Private: true, Loopback: true}},
		{"https://[fe80::1]/", Policy{Private: true, Loopback: true}},
		{"https://10.255.255.1/", Policy{}},
		{"https://127.0.0.1:1/", Policy{Private: true}},
	} {
		if err := get(tc.p.Client(), tc.url); !errors.Is(err, ErrRefused) {
			t.Errorf("%s with %+v: %v", tc.url, tc.p, err)
		}
	}
}

func TestProxy(t *testing.T) {
	var mu sync.Mutex
	var seen []string
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seen = append(seen, r.Method+" "+r.Host)
		mu.Unlock()
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer proxy.Close()
	t.Setenv("HTTPS_PROXY", "")
	t.Setenv("https_proxy", "")
	t.Setenv("ALL_PROXY", proxy.URL)
	t.Setenv("NO_PROXY", "")
	// The proxy is on loopback, which a public server's policy refuses:
	// the proxy's own address isn't checked. The configured server's name
	// may resolve only at the proxy.
	p, err := For(context.Background(), "https://mcp.example.invalid/mcp")
	if err != nil {
		t.Fatal(err)
	}
	_ = get(p.Client(), "https://mcp.example.invalid/mcp")
	mu.Lock()
	got := strings.Join(seen, ",")
	mu.Unlock()
	if got != "CONNECT mcp.example.invalid:443" {
		t.Errorf("the proxy saw %q", got)
	}
	// Through a proxy, a target at a refused address is refused here, and
	// so is another name that doesn't resolve here, such as one from
	// discovered metadata.
	if err := get(Policy{}.Client(), "https://169.254.169.254/"); !errors.Is(err, ErrRefused) {
		t.Errorf("a proxied request to the metadata address: %v", err)
	}
	if err := get(p.Client(), "https://internal.example.invalid/token"); !errors.Is(err, ErrRefused) {
		t.Errorf("a proxied request to another name that doesn't resolve: %v", err)
	}
	mu.Lock()
	got = strings.Join(seen, ",")
	mu.Unlock()
	if strings.Contains(got, "internal.example.invalid") {
		t.Errorf("the proxy saw %q", got)
	}
}

func TestOrigin(t *testing.T) {
	for in, want := range map[string]string{
		"HTTPS://Example.COM:443/mcp": "https://example.com",
		"http://127.0.0.1:80/x":       "http://127.0.0.1",
		"https://[::1]:8443/":         "https://[::1]:8443",
	} {
		u, _ := url.Parse(in)
		if got := Origin(u); got != want {
			t.Errorf("Origin(%s) = %s, want %s", in, got, want)
		}
	}
}
