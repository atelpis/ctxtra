package proxy

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"slices"
	"strconv"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/ctxtra/ctxtra/internal/mcp/creds"
	"github.com/ctxtra/ctxtra/internal/mcp/netguard"
	"github.com/ctxtra/ctxtra/internal/mcp/upstream"
)

// Upstream is a session with one bound server.
type Upstream struct {
	Server  upstream.Server
	Session *mcp.ClientSession
	cmd     *exec.Cmd // a local server's process; nil for a remote one
	once    sync.Once
}

// ExitState is how a local server's process ended, "" while it runs or
// for a remote server.
func (u *Upstream) ExitState() string {
	if u.cmd == nil || u.cmd.ProcessState == nil {
		return ""
	}
	return u.cmd.ProcessState.String()
}

// stopGrace is how long a local server has to exit after SIGTERM.
const stopGrace = 2 * time.Second

// Close ends the session. A local server's process group gets SIGTERM, and
// SIGKILL when it is still there after stopGrace.
func (u *Upstream) Close() {
	u.once.Do(func() {
		if u.cmd == nil || u.cmd.Process == nil {
			_ = u.Session.Close()
			return
		}
		pgid := u.cmd.Process.Pid
		signalGroup(pgid, false)
		done := make(chan struct{})
		go func() {
			// Closes the server's stdin and waits for it to exit.
			_ = u.Session.Close()
			close(done)
		}()
		grace := time.After(stopGrace)
		select {
		case <-done:
		case <-grace:
			signalGroup(pgid, true)
			<-done
			return
		}
		// The server exited; processes it started may still run in its
		// group.
		for groupAlive(pgid) {
			select {
			case <-grace:
				signalGroup(pgid, true)
				return
			case <-time.After(50 * time.Millisecond):
			}
		}
	})
}

// ErrSecrets is the error for a server whose secret values aren't
// available.
var ErrSecrets = errors.New("secret values are not available")

// Secrets are where servers' secret values come from: the credential
// store.
type Secrets interface {
	// Secrets are the values of s's SecretEnv and SecretHeaders, or an
	// error when one is missing or was set for another definition of s.
	Secrets(s upstream.Server) (env, headers map[string]string, err error)
}


// Dial starts a session with s, a server bound to a session in dir,
// without secret values.
func Dial(ctx context.Context, s upstream.Server, dir string, c *mcp.Client) (*Upstream, error) {
	return Dialer{}.Dial(ctx, s, dir, c)
}

// Dialer starts sessions with servers.
type Dialer struct {
	// Secrets has the servers' secret values; without it, a server that
	// has some can't start.
	Secrets Secrets
	// Tokens has remote servers' OAuth tokens; without it, requests go
	// without one.
	Tokens Tokens
	// Stderr gets a local server's stderr, for debugging; nil drops it.
	Stderr io.Writer
}

// Dial starts a session with s, a server bound to a session in dir. Only s
// gets its own secret values.
func (d Dialer) Dial(ctx context.Context, s upstream.Server, dir string, c *mcp.Client) (*Upstream, error) {
	if !s.Local() {
		return d.dialRemote(ctx, s, c)
	}
	var secrets map[string]string
	if len(s.SecretEnv) > 0 {
		if d.Secrets == nil {
			return nil, ErrSecrets
		}
		env, _, err := d.Secrets.Secrets(s)
		if err != nil {
			return nil, secretsError{err}
		}
		secrets = env
	}
	cmd := Command(s, dir, os.LookupEnv, secrets)
	if d.Stderr != nil {
		cmd.Stderr = d.Stderr
	}
	t := &startWatch{CommandTransport: mcp.CommandTransport{Command: cmd, TerminateDuration: stopGrace}, ctx: ctx}
	cs, err := c.Connect(ctx, t, nil)
	t.release()
	if err != nil {
		if cmd.Process != nil {
			signalGroup(cmd.Process.Pid, true)
		}
		if cmd.ProcessState != nil {
			return nil, exited{cmd.ProcessState.String(), err}
		}
		return nil, err
	}
	return &Upstream{Server: s, Session: cs, cmd: cmd}, nil
}

// startWatch stops the server's process group when ctx ends while the
// server starts: SIGTERM, then SIGKILL after stopGrace. The SDK's own
// cleanup of a failed start waits longer, and only for the server itself.
type startWatch struct {
	mcp.CommandTransport
	ctx  context.Context
	mu   sync.Mutex
	stop func() bool
}

func (t *startWatch) Connect(ctx context.Context) (mcp.Connection, error) {
	conn, err := t.CommandTransport.Connect(ctx)
	if err != nil {
		return nil, err
	}
	pgid := t.Command.Process.Pid
	t.mu.Lock()
	t.stop = context.AfterFunc(t.ctx, func() {
		signalGroup(pgid, false)
		time.AfterFunc(stopGrace, func() { signalGroup(pgid, true) })
	})
	t.mu.Unlock()
	return conn, nil
}

// release ends the watch once the server has started: Upstream.Close
// stops it from then on.
func (t *startWatch) release() {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.stop != nil {
		t.stop()
	}
}

// baseEnv are the variables every local server gets from the proxy's
// environment, as native MCP servers do: what a program needs to run, and
// to reach the network through a proxy.
var baseEnv = []string{"PATH", "HOME", "USER", "LANG", "TMPDIR",
	"HTTPS_PROXY", "HTTP_PROXY", "NO_PROXY", "ALL_PROXY", "https_proxy", "http_proxy", "no_proxy", "all_proxy",
	"SSL_CERT_FILE", "SSL_CERT_DIR"}

// Command is the process of the local server s, started in dir, the
// session folder, in a process group of its own so that it can be stopped
// with its children. Its environment is the base variables and env-pass
// names from lookup, then s.Env, then the secret values. Its stderr is
// dropped: it can hold secrets, and agents show none of it.
func Command(s upstream.Server, dir string, lookup func(string) (string, bool), secrets map[string]string) *exec.Cmd {
	cmd := exec.Command(s.Command[0], s.Command[1:]...)
	cmd.Dir = dir
	env := map[string]string{}
	for _, k := range slices.Concat(baseEnv, s.EnvPass) {
		if v, ok := lookup(k); ok {
			env[k] = v
		}
	}
	for k, v := range s.Env {
		env[k] = v
	}
	for k, v := range secrets {
		env[k] = v
	}
	cmd.Env = []string{}
	for k, v := range env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	slices.Sort(cmd.Env)
	cmd.Stderr = io.Discard
	setGroup(cmd)
	return cmd
}

// Record is one line of the proxy in ctxtra's log. It never has tool
// arguments, results or secrets.
type Record struct {
	Event    string   `json:"event"`
	Dir      string   `json:"dir,omitempty"`
	Server   string   `json:"server,omitempty"`
	Servers  []string `json:"servers,omitempty"`
	Problems int      `json:"problems,omitempty"`
	Tools    int      `json:"tools,omitempty"`
	Ms       int64    `json:"ms,omitempty"`
	Error    string   `json:"error,omitempty"`
}

// secretsError is a server whose secret values couldn't be read. Its text
// names the server and the reason, never a value.
type secretsError struct{ err error }

func (e secretsError) Error() string   { return ErrSecrets.Error() + ": " + e.err.Error() }
func (e secretsError) Unwrap() []error { return []error{ErrSecrets, e.err} }

// exited is the error of a server that ended before it answered.
type exited struct {
	state string
	err   error
}

func (e exited) Error() string { return "server " + e.state + ": " + e.err.Error() }
func (e exited) Unwrap() error { return e.err }

// errorKind is what the log says about err: its kind or code, never its
// message, which can repeat the arguments of a call.
func errorKind(err error) string {
	var exit *exec.ExitError
	var rpc *jsonrpc.Error
	var ended exited
	var login *creds.LoginError
	switch {
	case errors.As(err, &ended):
		return "server " + ended.state
	case errors.Is(err, context.Canceled):
		return "cancelled"
	case errors.Is(err, context.DeadlineExceeded):
		return "timeout"
	case errors.Is(err, ErrSecrets), errors.Is(err, ErrTooManyTools):
		// These say why without the server's own words.
		return err.Error()
	case errors.As(err, &login):
		return "needs sign-in: " + login.Reason
	case errors.Is(err, netguard.ErrRefused):
		return "refused by ctxtra's network rules"
	case errors.Is(err, exec.ErrNotFound), errors.Is(err, os.ErrNotExist):
		return "program not found"
	case errors.Is(err, exec.ErrDot):
		return "program found in a relative PATH entry"
	case errors.As(err, &exit):
		return "server exited: " + exit.String()
	case errors.Is(err, io.EOF), errors.Is(err, mcp.ErrConnectionClosed):
		return "connection closed"
	case errors.As(err, &rpc):
		return "error code " + strconv.FormatInt(rpc.Code, 10)
	}
	return "error"
}
