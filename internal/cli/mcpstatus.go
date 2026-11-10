package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"

	sessionstatus "github.com/ctxtra/ctxtra/internal/mcp/status"
	"github.com/ctxtra/ctxtra/internal/paths"
)

func (a *app) mcpStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status [DIR]",
		Short: "Show the MCP servers of a folder, and why a session has no tools of one",
		Long: `Show the MCP servers bound to DIR (the current folder by default) with their
sign-in state, and what the last session started there got from each: its
tools in time, later, or none, and why. Reads local files only.`,
		Example: `  ctxtra mcp status
  ctxtra mcp status ~/dev/app`,
		Args: atMost(1),
		RunE: a.mcpRun(func(_ *cobra.Command, args []string) error {
			dir, _ := os.Getwd()
			if len(args) == 1 {
				dir = paths.Resolve(dir, args[0])
			}
			if abs, err := filepath.Abs(dir); err == nil {
				dir = abs
			}
			return code(a.mcpStatus(dir))
		}),
	}
}

func (a *app) mcpStatus(dir string) int {
	servers, _ := sessionServers(dir)
	if len(servers) == 0 {
		fmt.Fprintf(a.Out, "no MCP servers are bound to %s\n", paths.Tilde(dir))
		return 0
	}
	rec, ok, err := sessionstatus.Read(dir)
	if err != nil {
		fmt.Fprintf(a.Err, "warning: can't read the last session's record: %v\n", err)
	}
	fmt.Fprintf(a.Out, "MCP servers of %s:\n", paths.Tilde(dir))
	for _, s := range servers {
		fmt.Fprintf(a.Out, "  %s  %s, %s\n", s.Name, describeServer(s), signIn(s))
	}
	if !ok {
		fmt.Fprintln(a.Out, "\nno session has started here with ctxtra's MCP server yet")
		return 0
	}
	fmt.Fprintf(a.Out, "\nthe last session here (%s, %s) waited %s for the first tool list:\n",
		rec.Agent, rec.Started.Local().Format(time.DateTime), seconds(rec.DeadlineMs))
	for _, s := range rec.Servers {
		switch s.State {
		case sessionstatus.Ready:
			fmt.Fprintf(a.Out, "  %s  ready after %s\n", s.Name, seconds(s.Ms))
		case sessionstatus.Late:
			fmt.Fprintf(a.Out, "  %s  later: %s, after %s; Codex and Muse sessions didn't get its tools\n", s.Name, s.Reason, seconds(s.Ms))
		default:
			fmt.Fprintf(a.Out, "  %s  missing: %s\n", s.Name, s.Reason)
		}
	}
	return 0
}

func seconds(ms int64) string {
	return fmt.Sprintf("%.1f s", float64(ms)/1000)
}
