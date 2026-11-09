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
	"syscall"
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
		_ = syscall.Kill(-pgid, syscall.SIGTERM)
		done := make(chan struct{})
		go func() {
			// Closes the server's stdin and waits for it to exit.
			_ = u.Session.Close()
			close(done)
		}()
		select {
		case <-done:
		case <-time.After(stopGrace):
			_ = syscall.Kill(-pgid, syscall.SIGKILL)
			<-done
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
	if !s.Local() {
		return nil, ErrRemote
	}
	if len(s.SecretEnv) > 0 {
		return nil, ErrSecrets
	}
	cmd := Command(s, dir, os.LookupEnv, nil)
	cs, err := c.Connect(ctx, &mcp.CommandTransport{Command: cmd, TerminateDuration: stopGrace}, nil)
	if err != nil {
		if cmd.Process != nil {
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		}
		return nil, err
	}
	return &Upstream{Server: s, Session: cs, cmd: cmd}, nil
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
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	return cmd
}

// Record is one line of the proxy in ctxtra's log. It never has tool
// arguments, results or secrets.
type Record struct {
	Event    string   `json:"event"`
	Dir      string   `json:"dir,omitempty"`
	Server   string   `json:"server,omitempty"`
	Servers  []string `json:"servers,omitempty"`
	Problems []string `json:"problems,omitempty"`
	Tools    int      `json:"tools,omitempty"`
	Ms       int64    `json:"ms,omitempty"`
	Error    string   `json:"error,omitempty"`
}

// errorKind is what the log says about err: its kind or code, never its
// message, which can repeat the arguments of a call.
func errorKind(err error) string {
	var exit *exec.ExitError
	var rpc *jsonrpc.Error
	switch {
	case errors.Is(err, context.Canceled):
		return "cancelled"
	case errors.Is(err, context.DeadlineExceeded):
		return "timeout"
	case errors.Is(err, ErrSecrets), errors.Is(err, ErrRemote):
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
