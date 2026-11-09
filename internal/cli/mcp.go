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
	"github.com/ctxtra/ctxtra/internal/mcp/names"
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
	c.AddCommand(a.mcpAddCmd(), a.mcpListCmd(), a.mcpShowCmd(), a.mcpRemoveCmd(), mcpProxyCmd())
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
	env, envPass []string
	notes        string
	noLogin      bool
	dryRun       bool
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
path or a name in PATH. A local server gets only its --env and --env-pass
variables and a few base ones (PATH, HOME, proxy settings), not the agent's.`,
		Example: `  ctxtra mcp add pg --env PGUSER=dev -- npx -y @modelcontextprotocol/server-postgres postgresql://localhost/dev
  ctxtra mcp add linear_work https://mcp.linear.app/mcp --no-login`,
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
			return code(mcpAdd(a.IO, args[0], url, command, o))
		}),
	}
	f := c.Flags()
	f.StringArrayVar(&o.env, "env", nil, "give a local server the variable `NAME=VALUE` (repeatable)")
	f.StringArrayVar(&o.envPass, "env-pass", nil, "pass a local server the variable `NAME` from the agent's environment (repeatable)")
	f.StringVar(&o.notes, "notes", "", "your notes about the server, such as which account to use")
	f.BoolVar(&o.noLogin, "no-login", false, "don't sign in now (remote servers)")
	f.BoolVar(&o.dryRun, "dry-run", false, "show the change; change nothing, start and connect to nothing")
	return c
}

func mcpAdd(stdio IO, name, url string, command []string, o mcpAddOptions) int {
	s := newSources(stdio)
	fail := func(err error) int { return s.fail("mcp add", err) }
	if err := config.CheckServerName(name); err != nil {
		return fail(err)
	}
	srv := config.NewMCPServer{Name: name, URL: url, Command: command, EnvPass: o.envPass, Notes: o.notes}
	for _, kv := range o.env {
		k, v, ok := strings.Cut(kv, "=")
		if !ok || k == "" {
			return fail(fmt.Errorf("--env %q: expected NAME=VALUE", kv))
		}
		srv.Env = append(srv.Env, [2]string{k, v})
	}
	if url != "" && !o.noLogin && !o.dryRun {
		return fail(errors.New("signing in to remote servers isn't supported yet; add --no-login"))
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
	if o.dryRun {
		old, err := readConfig(s.file)
		if err == nil {
			_, err = change(old)
		}
		if err != nil {
			return fail(err)
		}
		fmt.Fprintf(s.Out, "would add the MCP server %s to %s: %s\n", name, paths.Tilde(s.file), describeServer(added))
		fmt.Fprintln(s.Out, "nothing changed (--dry-run)")
		return 0
	}
	if err := s.edit(change); err != nil {
		return fail(err)
	}
	fmt.Fprintf(s.Out, "added the MCP server %s to %s: %s\n", name, paths.Tilde(s.file), describeServer(added))
	if added.Local() {
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
	up, err := proxy.DialStderr(ctx, s, dir, mcp.NewClient(&mcp.Implementation{Name: "ctxtra"}, &mcp.ClientOptions{Capabilities: &mcp.ClientCapabilities{}}), errOut)
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

// signIn is a server's sign-in state, from local files only.
func signIn(s upstream.Server) string {
	switch {
	case s.Local() && len(s.SecretEnv) == 0:
		return "none needed"
	case s.Local():
		return "secret values not set"
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
			out := []mcpInfo{}
			for _, s := range cfg.MCPServers {
				kind := "remote"
				if s.Local() {
					kind = "local"
				}
				out = append(out, mcpInfo{s.Name, kind, s.URL, s.Command, signIn(s), s.Notes})
			}
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
			var err error
			if dryRun {
				var old []byte
				if old, err = readConfig(s.file); err == nil {
					_, err = change(old)
				}
			} else {
				err = s.edit(change)
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
