package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"strings"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/ctxtra/ctxtra/internal/config"
	"github.com/ctxtra/ctxtra/internal/mcp/creds"
	"github.com/ctxtra/ctxtra/internal/mcp/netguard"
	"github.com/ctxtra/ctxtra/internal/mcp/oauth"
	"github.com/ctxtra/ctxtra/internal/mcp/upstream"
	"github.com/ctxtra/ctxtra/internal/paths"
)

// openStore is the credential store, sending token requests under each
// server's network rules.
func openStore() *creds.Store {
	s := creds.Open()
	s.Client = func(resource string) *http.Client {
		p, err := netguard.For(context.Background(), resource)
		if err != nil {
			// Nothing is reachable: For failed on the server's name.
			p = netguard.Policy{}
		}
		return p.Client()
	}
	return s
}

// errNeedsTerminal is a sign-in without a terminal to sign in from.
var errNeedsTerminal = errors.New("signing in needs a terminal")

type signInOptions struct {
	noBrowser    bool
	callbackPort int
	clientSecret string
}

// detectOAuth reports the 401 challenge of the remote server s, nil when
// it needs no sign-in. It sends s's headers, its secret ones from headers.
func detectOAuth(ctx context.Context, s upstream.Server, secretHeaders map[string]string) (*oauth.Challenge, error) {
	p, err := netguard.For(ctx, s.URL)
	if err != nil {
		return nil, err
	}
	h := map[string]string{}
	for k, v := range s.Headers {
		h[k] = v
	}
	for k, v := range secretHeaders {
		h[k] = v
	}
	return oauth.Detect(ctx, p.Client(), s.URL, h)
}

// signInTo signs in to s in the terminal, and says what it got.
func signInTo(ctx context.Context, stdio IO, store *creds.Store, s upstream.Server, ch *oauth.Challenge, o signInOptions) error {
	p, err := netguard.For(ctx, s.URL)
	if err != nil {
		return err
	}
	fileKey, err := store.EnsureKey()
	if err != nil {
		return err
	}
	if fileKey {
		warnFileKey(stdio.Err, store)
	}
	st, _ := store.State(s)
	var scopes []string
	r, _ := store.Load(s)
	secret := o.clientSecret
	port := o.callbackPort
	if r != nil && r.Matches(s) {
		scopes = append(scopes, r.Token.Scopes...)
		if secret == "" && r.Client.ID == s.ClientID {
			secret = r.Client.Secret
		}
		if port == 0 {
			port = r.Client.CallbackPort
		}
	}
	scopes = append(scopes, st.Scopes...)
	f := &oauth.SignIn{Server: s, Store: store, HTTP: p.Client(), Out: stdio.Err, In: stdio.In,
		OpenBrowser: openBrowser, NoBrowser: o.noBrowser, CallbackPort: port, ClientSecret: secret, Scopes: scopes}
	res, err := f.Run(ctx, ch)
	if err != nil {
		return err
	}
	fmt.Fprintf(stdio.Out, "signed in to %s with %s\n", s.Name, res.Issuer)
	if res.Account != "" {
		fmt.Fprintf(stdio.Out, "account: %s\n", res.Account)
	}
	if s.Notes != "" {
		fmt.Fprintf(stdio.Out, "notes: %s\n", s.Notes)
	}
	if res.NoRefresh {
		fmt.Fprintf(stdio.Out, "The provider gave no refresh token: when this sign-in expires, run ctxtra mcp login %s again.\n", s.Name)
	}
	return nil
}

// openBrowser opens u in the user's browser: $BROWSER if set, else the
// system's opener.
func openBrowser(u string) error {
	var cmd *exec.Cmd
	switch b := os.Getenv("BROWSER"); {
	case b != "":
		cmd = exec.Command(strings.Split(b, string(os.PathListSeparator))[0], u)
	case runtime.GOOS == "darwin":
		cmd = exec.Command("open", u)
	default:
		cmd = exec.Command("xdg-open", u)
	}
	cmd.Stdout, cmd.Stderr = io.Discard, io.Discard
	return cmd.Start()
}

// signalContext ends on Ctrl-C, so a waiting sign-in stops.
func signalContext() (context.Context, context.CancelFunc) {
	return signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
}

func (a *app) mcpLoginCmd() *cobra.Command {
	var o signInOptions
	var secrets bool
	c := &cobra.Command{
		Use:   "login NAME",
		Short: "Sign in to an MCP server, or set its secret values again",
		Long: `Sign in to a remote MCP server that uses OAuth, in your browser, and store the
tokens for every agent. For a server without OAuth, or with --secrets, ask for
its secret values again. Sessions that already run in Codex or Muse get the
server's tools in a new session; the others pick them up within seconds.`,
		Example: `  ctxtra mcp login linear_work
  ctxtra mcp login linear_work --no-browser
  ctxtra mcp login pg`,
		Args: exactly("NAME"),
		RunE: a.mcpRun(func(_ *cobra.Command, args []string) error {
			return code(a.mcpLogin(args[0], secrets, o))
		}),
	}
	f := c.Flags()
	f.BoolVar(&secrets, "secrets", false, "also ask for the secret values of an OAuth server")
	f.BoolVar(&o.noBrowser, "no-browser", false, "print the link instead of opening a browser, and accept the address it ends at")
	f.IntVar(&o.callbackPort, "callback-port", 0, "listen for the sign-in on this `PORT`, as your own app expects; it is stored")
	return c
}

func (a *app) mcpLogin(name string, withSecrets bool, o signInOptions) int {
	fail := func(err error) int {
		fmt.Fprintf(a.Err, "ctxtra mcp login: %v\n", err)
		return 1
	}
	cfg := config.Load(paths.ConfigPath(), newReader())
	s, ok := cfg.MCPServer(name)
	if !ok {
		return fail(fmt.Errorf("no MCP server %q in %s", name, paths.Tilde(cfg.File)))
	}
	store := openStore()
	ctx, stop := signalContext()
	defer stop()
	ask := func() error {
		v, err := askSecrets(a.IO, a.noInput, name, s.SecretEnv, s.SecretHeaders, false)
		if err != nil || v.empty() {
			return err
		}
		return storeSecrets(a.Err, store, s, v)
	}
	if s.Local() {
		if len(s.SecretEnv) == 0 {
			return fail(fmt.Errorf("%s is a local server without secret values: there is nothing to sign in to", name))
		}
		if err := ask(); err != nil {
			return fail(err)
		}
		fmt.Fprintf(a.Out, "stored the secret values of %s\n", name)
		return 0
	}
	_, headers, _ := store.Secrets(s)
	ch, err := detectOAuth(ctx, s, headers)
	if err != nil {
		return fail(err)
	}
	if ch == nil || withSecrets {
		if err := ask(); err != nil {
			return fail(err)
		}
		if ch == nil {
			fmt.Fprintf(a.Out, "%s needs no sign-in; stored its secret values\n", name)
			return 0
		}
	}
	if a.noInput || !Interactive(a.IO) {
		return fail(fmt.Errorf("%w: run ctxtra mcp login %s in your own terminal", errNeedsTerminal, name))
	}
	if err := signInTo(ctx, a.IO, store, s, ch, o); err != nil {
		return fail(err)
	}
	if o.callbackPort != 0 {
		_ = store.SetCallbackPort(s, o.callbackPort)
	}
	fmt.Fprintln(a.Out, "Sessions already running in Codex or Muse get its tools in a new session.")
	return 0
}

func (a *app) mcpLogoutCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "logout NAME",
		Short: "Delete an MCP server's sign-in and secret values on this machine",
		Long: `Delete the server's tokens and secret values on this machine, like the agents'
own mcp logout. The provider isn't told: the sign-in stays valid there until
it expires or you remove it in the provider's settings. The server stays in
the config.`,
		Example: `  ctxtra mcp logout linear_work`,
		Args:    exactly("NAME"),
		RunE: a.mcpRun(func(_ *cobra.Command, args []string) error {
			name := args[0]
			cfg := config.Load(paths.ConfigPath(), newReader())
			s, ok := cfg.MCPServer(name)
			if !ok {
				s = upstream.Server{ID: config.MCPServerID(name), Name: name}
			}
			if err := openStore().Logout(s); err != nil {
				fmt.Fprintf(a.Err, "ctxtra mcp logout: %v\n", err)
				return exitCode(1)
			}
			fmt.Fprintf(a.Out, "deleted the sign-in and secret values of %s on this machine\n", name)
			if !s.Local() {
				fmt.Fprintln(a.Out, "The provider wasn't told: the sign-in stays valid there until it expires or you remove it in its settings.")
			}
			return nil
		}),
	}
}
