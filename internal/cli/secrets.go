package cli

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	xterm "golang.org/x/term"

	"github.com/ctxtra/ctxtra/internal/mcp/creds"
	"github.com/ctxtra/ctxtra/internal/paths"
	"github.com/ctxtra/ctxtra/internal/term"
)

// secretReader asks for secret values: in the terminal without echo, or
// one line each from stdin when it isn't a terminal, for scripts.
type secretReader struct {
	stdio    IO
	noInput  bool
	optional bool // an empty value is an answer
	lines    *bufio.Reader
}

func (r *secretReader) read(prompt string) (string, error) {
	if f, ok := r.stdio.In.(*os.File); ok && term.IsTerminal(f) {
		if r.noInput {
			return "", errors.New("secret values are asked for in the terminal; leave out --no-input, or pipe them to stdin, one per line")
		}
		fmt.Fprintf(r.stdio.Err, "%s: ", prompt)
		v, err := xterm.ReadPassword(int(f.Fd()))
		fmt.Fprintln(r.stdio.Err)
		if err != nil {
			return "", err
		}
		return r.check(prompt, string(v))
	}
	if r.lines == nil {
		r.lines = bufio.NewReader(r.stdio.In)
	}
	v, err := r.lines.ReadString('\n')
	if err != nil && (!errors.Is(err, io.EOF) || v == "") {
		return "", fmt.Errorf("%s: no value on stdin", prompt)
	}
	return r.check(prompt, strings.TrimRight(v, "\r\n"))
}

// askClientSecret asks for the secret of the server's own OAuth app; an
// empty answer is an app without one.
func askClientSecret(stdio IO, noInput bool, server string) (string, error) {
	r := &secretReader{stdio: stdio, noInput: noInput, optional: true}
	return r.read("client secret for " + server + " (empty for an app without one)")
}

func (r *secretReader) check(prompt, v string) (string, error) {
	if r.optional {
		return v, nil
	}
	if v == "" {
		return "", fmt.Errorf("%s: the value is empty", prompt)
	}
	return v, nil
}

// secretValues are the values a new server's flags ask for.
type secretValues struct {
	env, headers map[string]string
	clientSecret string
}

func (v secretValues) empty() bool {
	return len(v.env) == 0 && len(v.headers) == 0 && v.clientSecret == ""
}

// askSecrets asks for the values of server's secret variables and headers,
// and its client secret when clientSecret is set.
func askSecrets(stdio IO, noInput bool, server string, env, headers []string, clientSecret bool) (secretValues, error) {
	r := &secretReader{stdio: stdio, noInput: noInput}
	out := secretValues{env: map[string]string{}, headers: map[string]string{}}
	for _, k := range env {
		v, err := r.read("value of " + k + " for " + server)
		if err != nil {
			return out, err
		}
		out.env[k] = v
	}
	for _, k := range headers {
		v, err := r.read("value of the header " + k + " for " + server)
		if err != nil {
			return out, err
		}
		out.headers[k] = v
	}
	if clientSecret {
		v, err := r.read("client secret for " + server)
		if err != nil {
			return out, err
		}
		out.clientSecret = v
	}
	return out, nil
}

// warnFileKey says that the key of the credentials is a file, which any
// process of the user can read.
func warnFileKey(w io.Writer, store *creds.Store) {
	fmt.Fprintf(w, "warning: no OS keychain is usable, so the key of ctxtra's credentials is in %s: any process of yours can read the credentials\n",
		paths.Tilde(store.KeyLocation()))
}
