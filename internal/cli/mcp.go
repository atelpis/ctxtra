package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"slices"
	"strings"
	"syscall"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/spf13/cobra"

	"github.com/ctxtra/ctxtra/internal/agent"
	"github.com/ctxtra/ctxtra/internal/agent/registry"
	"github.com/ctxtra/ctxtra/internal/config"
	"github.com/ctxtra/ctxtra/internal/experimental"
	"github.com/ctxtra/ctxtra/internal/mcp/creds"
	"github.com/ctxtra/ctxtra/internal/mcp/names"
	"github.com/ctxtra/ctxtra/internal/mcp/oauth"
	"github.com/ctxtra/ctxtra/internal/mcp/proxy"
	"github.com/ctxtra/ctxtra/internal/mcp/upstream"
	"github.com/ctxtra/ctxtra/internal/paths"
	"github.com/ctxtra/ctxtra/internal/setup"
	"github.com/ctxtra/ctxtra/internal/state"
)

// errMCPOff is the error of the mcp commands while MCP servers are off.
var errMCPOff = fmt.Errorf("MCP servers are experimental: set %s=1 to use them", experimental.MCPVar)

// mcpConnectTimeout bounds how long add and show wait for a server.
const mcpConnectTimeout = 30 * time.Second

func (a *app) mcpCmd() *cobra.Command {
	c := &cobra.Command{
		Use:                   "mcp",
		DisableFlagsInUseLine: true,
		Short:                 "Add, list, show and remove MCP servers (experimental)",
		Long: `MCP servers that sessions get by folder, like instructions and skills, through one
MCP server named ctxtra that ctxtra setup adds to each agent. Experimental: on
only with ` + experimental.MCPVar + `=1.`,
		Example: `  ctxtra mcp add pg -- npx -y @modelcontextprotocol/server-postgres postgresql://localhost/dev
  ctxtra mcp list`,
		GroupID: "config",
		Hidden:  !experimental.MCP(),
		RunE: func(c *cobra.Command, args []string) error {
			if !experimental.MCP() {
				fmt.Fprintf(a.Err, "ctxtra mcp: %v\n", errMCPOff)
				return exitCode(1)
			}
			return group(c, args)
		},
	}
	c.AddCommand(a.mcpAddCmd(), a.mcpListCmd(), a.mcpShowCmd(), a.mcpStatusCmd(), a.mcpLoginCmd(), a.mcpLogoutCmd(), a.mcpRemoveCmd(), mcpProxyCmd())
	return c
}

// mcpRun is a.run for the mcp commands: they refuse to run while MCP
// servers are off.
func (a *app) mcpRun(f func(cmd *cobra.Command, args []string) error) func(*cobra.Command, []string) error {
	return a.run(func(cmd *cobra.Command, args []string) error {
		if !experimental.MCP() {
			fmt.Fprintf(a.Err, "%s: %v\n", cmd.CommandPath(), errMCPOff)
			return exitCode(1)
		}
		return f(cmd, args)
	})
}

// mcpProxyCmd documents the proxy entry point. Run calls the proxy before
// the command tree, so this command never runs.
func mcpProxyCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "proxy AGENT",
		Short: "The MCP server that ctxtra setup adds to each agent",
		Long: `The command of the MCP server named ctxtra that ctxtra setup adds to each agent:
it serves the tools of the MCP servers bound to its working directory, the
session folder, and forwards each call. It ignores arguments it doesn't know
and never fails a session. You don't run it yourself.`,
		Hidden:                true,
		DisableFlagParsing:    true,
		DisableFlagsInUseLine: true,
		Run:                   func(*cobra.Command, []string) {},
	}
}

type mcpAddOptions struct {
	env, envPass  []string
	secretEnv     []string
	secretHeaders []string
	clientID      string
	clientSecret  bool
	notes         string
	noLogin       bool
	dryRun        bool
	noInput       bool
	interactive   bool
	signIn        signInOptions
}

func (a *app) mcpAddCmd() *cobra.Command {
	var o mcpAddOptions
	c := &cobra.Command{
		Use:   "add NAME (URL | -- COMMAND [ARG]...)",
		Short: "Add an MCP server to the config",
		Long: `Add an MCP server to the config: a local one, started as COMMAND in the
session folder, or a remote one at URL. Then it lists the tools that sessions
will see. Bind the server to folders with ctxtra binding add --mcp NAME.

NAME is 1 to 20 characters of a-z, 0-9 and _. The program must be an absolute
path or a name in PATH. A local server gets only its --env, --env-pass and
--secret-env variables and a few base ones (PATH, HOME, proxy settings), not
the agent's.

A remote server that uses OAuth is signed in to right away, in your browser,
unless --no-login is given; signing in needs a terminal.

ctxtra asks for the values of --secret-env, --secret-header and
--client-secret in the terminal, or reads them from stdin, one per line, and
stores them encrypted, never in the config.`,
		Example: `  ctxtra mcp add pg --env PGUSER=dev --secret-env PGPASSWORD -- npx -y @modelcontextprotocol/server-postgres postgresql://localhost/dev
  ctxtra mcp add linear_work https://mcp.linear.app/mcp --notes 'the work workspace'
  ctxtra mcp add internal https://mcp.internal.example/mcp --secret-header Authorization --no-login`,
		Args: func(c *cobra.Command, args []string) error {
			if len(args) == 0 {
				return errors.New("expected NAME")
			}
			return nil
		},
		RunE: a.mcpRun(func(c *cobra.Command, args []string) error {
			dash := c.ArgsLenAtDash()
			var url string
			var command []string
			switch {
			case dash == 1 && len(args) > 1:
				command = args[1:]
			case dash < 0 && len(args) == 2:
				url = args[1]
			case dash < 0 && len(args) > 2:
				return fmt.Errorf("unexpected argument %q; put a local server's command after --", args[2])
			default:
				return errors.New("expected a URL, or -- and a command")
			}
			o.noInput, o.interactive = a.noInput, !a.noInput && Interactive(a.IO)
			return code(mcpAdd(a.IO, args[0], url, command, o))
		}),
	}
	f := c.Flags()
	f.StringArrayVar(&o.env, "env", nil, "give a local server the variable `NAME=VALUE` (repeatable)")
	f.StringArrayVar(&o.envPass, "env-pass", nil, "pass a local server the variable `NAME` from the agent's environment (repeatable)")
	f.StringArrayVar(&o.secretEnv, "secret-env", nil, "give a local server the variable `NAME`, with a value ctxtra asks for and stores (repeatable)")
	f.StringArrayVar(&o.secretHeaders, "secret-header", nil, "send a remote server the header `NAME`, with a value ctxtra asks for and stores (repeatable)")
	f.StringVar(&o.clientID, "client-id", "", "the `ID` of your own OAuth app for a remote server")
	f.BoolVar(&o.clientSecret, "client-secret", false, "ask for the secret of the --client-id app, and store it")
	f.StringVar(&o.notes, "notes", "", "your notes about the server, such as which account to use")
	f.BoolVar(&o.noLogin, "no-login", false, "don't sign in now (remote servers)")
	f.BoolVar(&o.signIn.noBrowser, "no-browser", false, "print the sign-in link instead of opening a browser, and accept the address it ends at")
	f.IntVar(&o.signIn.callbackPort, "callback-port", 0, "listen for the sign-in on this `PORT`, as your own app expects; it is stored")
	f.BoolVar(&o.dryRun, "dry-run", false, "show the change; change nothing, start and connect to nothing")
	return c
}

func mcpAdd(stdio IO, name, url string, command []string, o mcpAddOptions) int {
	s := newSources(stdio)
	fail := func(err error) int { return s.fail("mcp add", err) }
	if err := config.CheckServerName(name); err != nil {
		return fail(err)
	}
	srv := config.NewMCPServer{Name: name, URL: url, Command: command, EnvPass: o.envPass, SecretEnv: o.secretEnv,
		SecretHeaders: o.secretHeaders, ClientID: o.clientID, Notes: o.notes}
	if o.clientSecret && o.clientID == "" {
		return fail(errors.New("--client-secret needs --client-id"))
	}
	for _, kv := range o.env {
		k, v, ok := strings.Cut(kv, "=")
		if !ok || k == "" {
			return fail(fmt.Errorf("--env %q: expected NAME=VALUE", kv))
		}
		srv.Env = append(srv.Env, [2]string{k, v})
	}
	var added upstream.Server
	change := func(old []byte) ([]byte, error) {
		out, err := config.AddMCPServer(old, srv)
		if err != nil {
			return nil, err
		}
		cfg := config.Load(s.file, withConfig{s.file, out, newReader()})
		var ok bool
		if added, ok = cfg.MCPServer(name); !ok {
			var problems []string
			for _, p := range cfg.Problems {
				if strings.Contains(p, "mcp-servers."+name+":") {
					problems = append(problems, reLine.ReplaceAllString(p, ""))
				}
			}
			return nil, fmt.Errorf("the server would be skipped; nothing changed:\n  %s", strings.Join(problems, "\n  "))
		}
		return out, nil
	}
	old, err := readConfig(s.file)
	if err == nil {
		_, err = change(old)
	}
	if err != nil {
		return fail(err)
	}
	if o.dryRun {
		fmt.Fprintf(s.Out, "would add the MCP server %s to %s: %s\n", name, paths.Tilde(s.file), describeServer(added))
		fmt.Fprintln(s.Out, "nothing changed (--dry-run)")
		return 0
	}
	secrets, err := askSecrets(stdio, o.noInput, name, added.SecretEnv, added.SecretHeaders, o.clientSecret)
	if err != nil {
		return fail(err)
	}
	ctx, stop := signalContext()
	defer stop()
	// A remote server that answers a request without a token with 401
	// uses OAuth: sign in now, in the terminal, or later with login.
	var ch *oauth.Challenge
	if !added.Local() && !o.noLogin {
		if ch, err = detectOAuth(ctx, added, secrets.headers); err != nil {
			return fail(fmt.Errorf("can't reach %s: %w; to add it anyway, add --no-login", name, err))
		}
		if ch != nil && !o.interactive {
			return fail(fmt.Errorf("%w; nothing changed: add --no-login, and run ctxtra mcp login %s in your terminal later", errNeedsTerminal, name))
		}
	}
	// The values are stored under the config's lock, once the edit knows
	// the name is new, so that a concurrent add of the same name can't
	// replace or delete them.
	store := openStore()
	commit := func(old []byte) ([]byte, error) {
		out, err := change(old)
		if err != nil || secrets.empty() && o.signIn.callbackPort == 0 {
			return out, err
		}
		if err := storeSecrets(s.Err, store, added, secrets); err != nil {
			return nil, err
		}
		if o.signIn.callbackPort != 0 {
			return out, store.SetCallbackPort(added, o.signIn.callbackPort)
		}
		return out, nil
	}
	if err := s.edit(commit); err != nil {
		return fail(err)
	}
	fmt.Fprintf(s.Out, "added the MCP server %s to %s: %s\n", name, paths.Tilde(s.file), describeServer(added))
	if !secrets.empty() {
		fmt.Fprintf(s.Out, "stored its secret values, encrypted, in %s\n", paths.Tilde(store.Dir))
	}
	signedIn := ch == nil
	if ch != nil {
		o.signIn.clientSecret = secrets.clientSecret
		if err := signInTo(ctx, stdio, store, added, ch, o.signIn); err != nil {
			fmt.Fprintf(s.Err, "ctxtra mcp add: the sign-in failed: %v\nThe server is added; sign in with ctxtra mcp login %s\n", err, name)
		} else {
			signedIn = true
		}
	}
	if added.Local() || !o.noLogin && signedIn {
		wd, _ := os.Getwd()
		if tools, err := serverTools(added, wd, false); err != nil {
			fmt.Fprintf(s.Err, "warning: couldn't list its tools: %v; check it with ctxtra mcp show %s\n", err, name)
		} else {
			printTools(s.Out, tools)
		}
	}
	fmt.Fprintf(s.Out, "\nNext: bind it to folders, for example ctxtra binding add %s --dir '~/dev/**' --mcp %s\n", name, name)
	if stale := agentsNeedingSetup(); len(stale) > 0 {
		fmt.Fprintf(s.Out, "and run ctxtra setup: ctxtra's MCP server in %s needs it\n", strings.Join(stale, ", "))
	}
	return 0
}

// agentsNeedingSetup are the agents set up for ctxtra whose MCP entry is
// missing or out of date for the config, such as Codex's env_vars for a
// new env-pass name.
func agentsNeedingSetup() []string {
	setups, _, _ := state.LoadSetups()
	mcp := setup.LoadMCP()
	var out []string
	for _, a := range registry.All() {
		m, ok := a.(agent.MCPAgent)
		if !ok || !setups.Known(a.Name(), a.ConfigPath()) {
			continue
		}
		if len(m.MCP().CheckMCP("", agent.MCPCheck{Want: true, EnvPass: mcp.EnvPass})) > 0 {
			out = append(out, a.Name())
		}
	}
	return out
}

// storeSecrets stores the secret values of srv, creating the store's key
// when there is none.
func storeSecrets(errOut io.Writer, store *creds.Store, srv upstream.Server, v secretValues) error {
	fileKey, err := store.EnsureKey()
	if err != nil {
		return err
	}
	if fileKey {
		warnFileKey(errOut, store)
	}
	if len(v.env) > 0 || len(v.headers) > 0 {
		if err := store.SetSecrets(srv, v.env, v.headers); err != nil {
			return err
		}
	}
	if v.clientSecret != "" {
		return store.SetClientSecret(srv, v.clientSecret)
	}
	return nil
}

func describeServer(s upstream.Server) string {
	if s.Local() {
		return "local, " + strings.Join(s.Command, " ")
	}
	return "remote, " + s.URL
}

// serverTools connects to s, started in dir, and names its tools as
// sessions see them. With stderr, a local server's stderr goes to ours.
func serverTools(s upstream.Server, dir string, stderr bool) ([]names.Name, error) {
	// A signal stops the server with every process it started.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT, syscall.SIGHUP)
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, mcpConnectTimeout)
	defer cancel()
	var errOut io.Writer
	if stderr {
		errOut = os.Stderr
	}
	// Started and stopped like the proxy starts and stops it, with every
	// process it starts.
	store := openStore()
	d := proxy.Dialer{Secrets: store, Tokens: store, Stderr: errOut}
	up, err := d.Dial(ctx, s, dir, mcp.NewClient(&mcp.Implementation{Name: "ctxtra"}, &mcp.ClientOptions{Capabilities: &mcp.ClientCapabilities{}}))
	if err != nil {
		return nil, err
	}
	defer up.Close()
	list, err := proxy.ListTools(ctx, up.Session)
	if err != nil {
		return nil, err
	}
	var tools []names.Tool
	for _, t := range list {
		tools = append(tools, names.Tool{Server: s.Name, Name: t.Name})
	}
	named, _ := names.Assign(tools)
	return named, nil
}

func printTools(w interface{ Write([]byte) (int, error) }, tools []names.Name) {
	if len(tools) == 0 {
		fmt.Fprintln(w, "it has no tools")
		return
	}
	fmt.Fprintln(w, "tools sessions see:")
	for _, t := range tools {
		if t.Shortened {
			fmt.Fprintf(w, "  %s (shortened from %s)\n", t.Shown, t.Name)
		} else {
			fmt.Fprintf(w, "  %s\n", t.Shown)
		}
	}
}

// mcpInfo is a server in mcp list --json.
type mcpInfo struct {
	Name    string   `json:"name"`
	Kind    string   `json:"kind"`
	URL     string   `json:"url,omitempty"`
	Command []string `json:"command,omitempty"`
	SignIn  string   `json:"sign_in"`
	Notes   string   `json:"notes,omitempty"`
}

// mcpInfos are the config's servers with their sign-in state.
func mcpInfos(cfg *config.Config) []mcpInfo {
	out := []mcpInfo{}
	for _, s := range cfg.MCPServers {
		kind := "remote"
		if s.Local() {
			kind = "local"
		}
		out = append(out, mcpInfo{s.Name, kind, s.URL, s.Command, signIn(s), s.Notes})
	}
	return out
}

// keyLocation is where the key of the credentials is: "keychain", the key
// file's path, or "" when nothing is stored yet.
func keyLocation() string {
	switch loc := creds.Open().KeyLocation(); loc {
	case "the OS keychain":
		return "keychain"
	default:
		return loc
	}
}

// keyNote says where the key of the credentials is, with the warning for
// a key file.
func keyNote() string {
	switch loc := keyLocation(); loc {
	case "":
		return "none stored yet"
	case "keychain":
		return "encrypted, the key in the OS keychain"
	default:
		return "encrypted, the key in " + paths.Tilde(loc) + " (no OS keychain was usable: any process of yours can read the credentials)"
	}
}

// signIn is a server's sign-in state, from local files only.
func signIn(s upstream.Server) string {
	switch {
	case s.Local() && len(s.SecretEnv) == 0:
		return "none needed"
	}
	st, err := creds.Open().State(s)
	switch {
	case err != nil:
		return "sign-in state unknown: " + err.Error()
	case s.Local() && st.SignedIn:
		return "secret values set"
	case s.Local():
		return "secret values not set"
	case st.NeedsLogin:
		return "needs sign-in: " + st.Reason
	case st.SignedIn && st.Issuer != "":
		return "signed in with " + st.Issuer
	case st.SignedIn:
		return "secret values set"
	}
	return "not signed in"
}

func (a *app) mcpListCmd() *cobra.Command {
	var asJSON bool
	c := &cobra.Command{
		Use:     "list",
		Short:   "List the MCP servers, with their sign-in state",
		Example: "  ctxtra mcp list\n  ctxtra mcp list --json",
		Args:    noArgs,
		RunE: a.mcpRun(func(*cobra.Command, []string) error {
			cfg := config.Load(paths.ConfigPath(), newReader())
			out := mcpInfos(cfg)
			var problems []string
			for _, p := range cfg.Problems {
				if strings.Contains(p, "mcp-servers") {
					problems = append(problems, p)
				}
			}
			if asJSON {
				writeJSON(a.Out, out, diagnostics("error", problems))
				return nil
			}
			if len(out) == 0 {
				fmt.Fprintf(a.Out, "no MCP servers in %s\n", paths.Tilde(cfg.File))
			}
			for _, s := range out {
				fmt.Fprintf(a.Out, "%s  %s, %s\n", s.Name, s.Kind, s.SignIn)
			}
			if len(out) > 0 {
				fmt.Fprintf(a.Out, "credentials: %s\n", keyNote())
			}
			for _, p := range problems {
				fmt.Fprintf(a.Out, "PROBLEM: %s\n", p)
			}
			return nil
		}),
	}
	c.Flags().BoolVar(&asJSON, "json", false, "print JSON")
	return c
}

func (a *app) mcpShowCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "show NAME",
		Short: "Connect to an MCP server and list the tools sessions see",
		Long: `Start or connect to the server, and list its tools with the names that sessions
see. A local server runs in the current folder, with its stderr on yours, to
debug it: an agent that runs this command sees that output too.`,
		Example: `  ctxtra mcp show pg`,
		Args:    exactly("NAME"),
		RunE: a.mcpRun(func(_ *cobra.Command, args []string) error {
			cfg := config.Load(paths.ConfigPath(), newReader())
			s, ok := cfg.MCPServer(args[0])
			if !ok {
				fmt.Fprintf(a.Err, "ctxtra mcp show: no MCP server %q in %s\n", args[0], paths.Tilde(cfg.File))
				return exitCode(1)
			}
			fmt.Fprintf(a.Out, "%s: %s\n", s.Name, describeServer(s))
			if s.Notes != "" {
				fmt.Fprintf(a.Out, "notes: %s\n", s.Notes)
			}
			wd, _ := os.Getwd()
			tools, err := serverTools(s, wd, true)
			if err != nil {
				fmt.Fprintf(a.Err, "ctxtra mcp show: %s: %v\n", s.Name, err)
				return exitCode(1)
			}
			printTools(a.Out, tools)
			return nil
		}),
	}
}

func (a *app) mcpRemoveCmd() *cobra.Command {
	var dryRun bool
	c := &cobra.Command{
		Use:     "remove NAME",
		Short:   "Remove an MCP server from the config, with its credentials",
		Example: `  ctxtra mcp remove pg`,
		Args:    exactly("NAME"),
		RunE: a.mcpRun(func(_ *cobra.Command, args []string) error {
			name := args[0]
			s := newSources(a.IO)
			change := func(old []byte) ([]byte, error) { return config.RemoveMCPServer(old, name) }
			// Its credentials go with it, also when its definition was
			// broken: after the config is written, under its lock, so a
			// later add of the same name keeps its own.
			deleteCreds := func() error {
				if err := creds.Open().Delete(upstream.Server{ID: config.MCPServerID(name), Name: name}); err != nil {
					return fmt.Errorf("removed it from the config, but couldn't delete its credentials: %w", err)
				}
				return nil
			}
			var err error
			if dryRun {
				var old []byte
				if old, err = readConfig(s.file); err == nil {
					_, err = change(old)
				}
			} else {
				err = s.editThen(change, deleteCreds)
			}
			if err != nil {
				return exitCode(s.fail("mcp remove", err))
			}
			verb := "removed"
			if dryRun {
				verb = "would remove"
			}
			fmt.Fprintf(a.Out, "%s the MCP server %s from %s\n", verb, name, paths.Tilde(s.file))
			cfg := config.Load(paths.ConfigPath(), newReader())
			// After the change, a binding that names the server has a problem
			// for it; before it (--dry-run), the binding still has it.
			for _, b := range cfg.Bindings {
				if dryRun && slices.Contains(b.MCPServers, name) {
					fmt.Fprintf(a.Err, "warning: the binding %s names %s, which would be unknown\n", b.ID(), name)
				}
			}
			for _, p := range cfg.Problems {
				if !dryRun && strings.Contains(p, "server "+name+" is not in mcp-servers") {
					fmt.Fprintf(a.Err, "warning: a binding still names %s, which is now unknown: %s\n", name, p)
				}
			}
			if dryRun {
				fmt.Fprintln(a.Out, "nothing changed (--dry-run)")
			}
			return nil
		}),
	}
	c.Flags().BoolVar(&dryRun, "dry-run", false, "show the change; change nothing")
	return c
}
