package creds

import (
	"bufio"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/zalando/go-keyring"

	"github.com/ctxtra/ctxtra/internal/fsutil"
	"github.com/ctxtra/ctxtra/internal/mcp/oauthtest"
	"github.com/ctxtra/ctxtra/internal/mcp/upstream"
)

// The test binary is also a helper process when this variable names a
// task; the other CREDS_ variables say for what.
const helperVar = "CTXTRA_CREDS_HELPER"

func TestMain(m *testing.M) {
	if task := os.Getenv(helperVar); task != "" {
		os.Exit(helper(task))
	}
	os.Exit(m.Run())
}

// helper runs one task in a process of its own, with the file key the
// test made (never the user's keychain), trusting the fake server's
// certificate through the file in CREDS_CA. It prints the token it got,
// or the error.
func helper(task string) int {
	keyring.MockInitWithError(errNoKeychain)
	pool := x509.NewCertPool()
	ca, _ := os.ReadFile(os.Getenv("CREDS_CA"))
	pool.AppendCertsFromPEM(ca)
	s := &Store{Dir: os.Getenv("CREDS_DIR"),
		HTTP: &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool}}}}
	srv := upstream.Server{ID: "linear_work", Name: "linear_work", URL: os.Getenv("CREDS_URL")}
	switch task {
	case "refresh":
		tok, err := s.Token(context.Background(), srv)
		if err != nil {
			fmt.Println("error:", err)
			return 1
		}
		fmt.Println(tok)
	case "login":
		var t Token
		_ = json.Unmarshal([]byte(os.Getenv("CREDS_TOKEN")), &t)
		r, err := s.Load(srv)
		if err != nil || r == nil {
			fmt.Println("error:", err)
			return 1
		}
		if _, err := s.SaveSignIn(srv, r.Issuer, r.Client, t); err != nil {
			fmt.Println("error:", err)
			return 1
		}
	case "remove":
		if err := s.Delete(srv); err != nil {
			fmt.Println("error:", err)
			return 1
		}
	case "hold":
		// Takes the server's lock and waits to be killed.
		if _, err := fsutil.Lock(s.lockPath(fileID(srv.ID))); err != nil {
			return 1
		}
		fmt.Println("locked")
		time.Sleep(time.Minute)
	}
	return 0
}

type procs struct {
	t    *testing.T
	env  []string
	mu   sync.Mutex
	outs []string
	wg   sync.WaitGroup
}

func newProcs(t *testing.T, fake *oauthtest.Server, dir string, extra ...string) *procs {
	ca := filepath.Join(t.TempDir(), "ca.pem")
	os.WriteFile(ca, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: fake.Certificate().Raw}), 0o600)
	return &procs{t: t, env: append(os.Environ(), append([]string{"CREDS_DIR=" + dir, "CREDS_CA=" + ca,
		"CREDS_URL=" + fake.ResourceURL()}, extra...)...)}
}

func (p *procs) cmd(task string, extra ...string) *exec.Cmd {
	cmd := exec.Command(os.Args[0], "-test.run=^$")
	cmd.Env = slices.Concat(p.env, []string{helperVar + "=" + task}, extra)
	return cmd
}

// start runs n helpers of task at once, in the background.
func (p *procs) start(task string, n int, extra ...string) {
	for range n {
		cmd := p.cmd(task, extra...)
		p.wg.Go(func() {
			out, _ := cmd.CombinedOutput()
			p.mu.Lock()
			p.outs = append(p.outs, task+": "+strings.TrimSpace(string(out)))
			p.mu.Unlock()
		})
	}
}

func (p *procs) wait() []string {
	p.wg.Wait()
	outs := p.outs
	p.outs = nil
	return outs
}

func fileKeyStore(t *testing.T, fake *oauthtest.Server) (*Store, upstream.Server) {
	t.Helper()
	keyring.MockInitWithError(errNoKeychain)
	fake.AddClient("app", "")
	s := &Store{Dir: t.TempDir(), HTTP: fake.Client()}
	if fileKey, err := s.EnsureKey(); err != nil || !fileKey {
		t.Fatalf("EnsureKey: %v %v", fileKey, err)
	}
	srv := upstream.Server{ID: "linear_work", Name: "linear_work", URL: fake.ResourceURL()}
	saveSignIn(t, s, srv, fake, fake.SignIn("app", "read"))
	return s, srv
}

func TestProcessesRefreshAtOnce(t *testing.T) {
	if testing.Short() {
		t.Skip("starts processes")
	}
	fake := oauthtest.New(t, oauthtest.Options{Lifetime: time.Hour, RefreshDelay: 50 * time.Millisecond})
	s, srv := fileKeyStore(t, fake)
	p := newProcs(t, fake, s.Dir)
	for round := 1; round <= 3; round++ {
		expireToken(t, s, srv, time.Minute)
		p.start("refresh", 6)
		outs := p.wait()
		for _, o := range outs {
			if o != outs[0] || strings.Contains(o, "error") {
				t.Fatalf("round %d: %q", round, outs)
			}
		}
		if fake.Refreshes() != round {
			t.Fatalf("round %d: %d refreshes", round, fake.Refreshes())
		}
	}
}

func TestProcessRefreshRacesLogin(t *testing.T) {
	if testing.Short() {
		t.Skip("starts processes")
	}
	fake := oauthtest.New(t, oauthtest.Options{Lifetime: time.Hour, RefreshDelay: 300 * time.Millisecond})
	s, srv := fileKeyStore(t, fake)
	expireToken(t, s, srv, time.Minute)
	login := fake.SignIn("app", "read", "write")
	tok, _ := json.Marshal(newToken(tokenResponse{AccessToken: login.AccessToken, RefreshToken: login.RefreshToken,
		ExpiresIn: json.Number(itoa(login.ExpiresIn))}, time.Now()))
	p := newProcs(t, fake, s.Dir)
	p.start("refresh", 3)
	time.Sleep(100 * time.Millisecond)
	p.start("login", 1, "CREDS_TOKEN="+string(tok))
	for _, o := range p.wait() {
		if strings.Contains(o, "error") {
			t.Errorf("%s", o)
		}
	}
	r, err := s.Load(srv)
	if err != nil || r.Token.Access != login.AccessToken || r.Generation != 2 {
		t.Fatalf("the new sign-in was lost: %+v %v", r, err)
	}
	// Later refreshes use the new sign-in's refresh token.
	expireToken(t, s, srv, time.Minute)
	p.start("refresh", 2)
	for _, o := range p.wait() {
		if strings.Contains(o, "error") {
			t.Errorf("%s", o)
		}
	}
}

func TestProcessRefreshRacesRemove(t *testing.T) {
	if testing.Short() {
		t.Skip("starts processes")
	}
	fake := oauthtest.New(t, oauthtest.Options{Lifetime: time.Hour, RefreshDelay: 300 * time.Millisecond})
	s, srv := fileKeyStore(t, fake)
	expireToken(t, s, srv, time.Minute)
	p := newProcs(t, fake, s.Dir)
	p.start("refresh", 3)
	time.Sleep(100 * time.Millisecond)
	p.start("remove", 1)
	p.wait()
	if r, err := s.Load(srv); r != nil || err != nil {
		t.Errorf("the removed record came back: %+v %v", r, err)
	}
	if _, err := os.Stat(s.statePath(fileID(srv.ID))); !os.IsNotExist(err) {
		t.Errorf("state: %v", err)
	}
}

func TestProcessDiesHoldingTheLock(t *testing.T) {
	if testing.Short() {
		t.Skip("starts processes")
	}
	fake := oauthtest.New(t, oauthtest.Options{Lifetime: time.Hour})
	s, srv := fileKeyStore(t, fake)
	expireToken(t, s, srv, time.Minute)
	p := newProcs(t, fake, s.Dir)
	hold := p.cmd("hold")
	out, _ := hold.StdoutPipe()
	if err := hold.Start(); err != nil {
		t.Fatal(err)
	}
	if line, _ := bufio.NewReader(out).ReadString('\n'); line != "locked\n" {
		t.Fatalf("hold: %q", line)
	}
	hold.Process.Kill()
	hold.Wait()
	p.start("refresh", 1)
	if outs := p.wait(); strings.Contains(outs[0], "error") || fake.Refreshes() != 1 {
		t.Errorf("after a crash: %q, %d refreshes", outs, fake.Refreshes())
	}
}
