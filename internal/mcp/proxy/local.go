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

// ErrRemote is the error for a remote server, which this version can't
// connect to yet.
var ErrRemote = errors.New("remote servers are not supported yet")

// Dial starts a session with s, a server bound to a session in dir.
func Dial(ctx context.Context, s upstream.Server, dir string, c *mcp.Client) (*Upstream, error) {
	return DialStderr(ctx, s, dir, c, nil)
}

// DialStderr is Dial with a local server's stderr going to stderr, for
// debugging; nil drops it.
func DialStderr(ctx context.Context, s upstream.Server, dir string, c *mcp.Client, stderr io.Writer) (*Upstream, error) {
	if !s.Local() {
		return nil, ErrRemote
	}
	if len(s.SecretEnv) > 0 {
		return nil, ErrSecrets
	}
	cmd := Command(s, dir, os.LookupEnv, nil)
	if stderr != nil {
		cmd.Stderr = stderr
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
	switch {
	case errors.As(err, &ended):
		return "server " + ended.state
	case errors.Is(err, context.Canceled):
		return "cancelled"
	case errors.Is(err, context.DeadlineExceeded):
		return "timeout"
	case errors.Is(err, ErrSecrets), errors.Is(err, ErrRemote), errors.Is(err, ErrTooManyTools):
		return err.Error()
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
