package cli

import (
	"context"
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/atelpis/ctxtra/internal/config"
	"github.com/atelpis/ctxtra/internal/fsutil"
	"github.com/atelpis/ctxtra/internal/paths"
	"github.com/atelpis/ctxtra/internal/source"
	"github.com/atelpis/ctxtra/internal/state"
	"github.com/atelpis/ctxtra/internal/update"
)

// backgroundTimeout ends a check that a session started. Each request has a
// shorter timeout of its own; this one only ends a check that hangs, with
// room for many sources that wait for each other.
const backgroundTimeout = 15 * time.Minute

func (a *app) checkUpdatesCmd() *cobra.Command {
	var (
		background bool
		sel        update.Selection
	)
	c := &cobra.Command{
		Use:   "check-updates",
		Short: "Check for a new ctxtra release and new commits of sources",
		Long: `Check for a new ctxtra release (unless the config has check-updates: false),
and for new commits of every pinned source with check-updates: true, and save
the results for sessions. It reads only version numbers and commit IDs: it
fetches no files, accepts nothing, and git never asks for credentials. Sessions
run this check in the background every 4 hours at most; run it to check now. It exits
with 1 when a check fails.`,
		Example: "  ctxtra check-updates",
		Args:    noArgs,
		GroupID: "inspect",
		RunE: a.run(func(*cobra.Command, []string) error {
			source.NoPrompt = true
			cfg := config.Load(paths.ConfigPath(), fsutil.NewReader(config.MaxFileBytes, config.MaxSessionBytes))
			if background {
				ctx, cancel := context.WithTimeout(context.Background(), backgroundTimeout)
				defer cancel()
				_, _ = update.Run(ctx, cfg, a.version, &sel)
				return nil
			}
			return code(a.checkUpdates(cfg))
		}),
	}
	// What the session that started the check claimed.
	f := c.Flags()
	f.BoolVar(&background, "background", false, "print nothing, and check only what the other flags select; for sessions")
	f.BoolVar(&sel.Release, "release", false, "with --background: check for a new release")
	f.StringArrayVar(&sel.Sources, "source", nil, "with --background: check the source `NAME` (repeatable)")
	for _, name := range []string{"background", "release", "source"} {
		_ = f.MarkHidden(name)
	}
	return c
}

func (a *app) checkUpdates(cfg *config.Config) int {
	for _, p := range cfg.Problems {
		fmt.Fprintf(a.Err, "warning: %s\n", p)
	}
	switch {
	case cfg.Ignored():
		fmt.Fprintln(a.Out, "nothing checked: the config can't be used (see the warning above)")
		return 1
	case !cfg.CheckCtxtra && len(update.Sources(cfg)) == 0:
		fmt.Fprintln(a.Out, "nothing to check: the config has check-updates: false, and no source has check-updates: true")
		return 0
	}
	results, werr := update.Run(context.Background(), cfg, a.version, nil)
	n := 0
	if werr != nil {
		fmt.Fprintf(a.Err, "ctxtra check-updates: can't save the results in %s, so sessions won't show them: %v\n", paths.Tilde(state.Path()), werr)
		n = 1
	}
	// Shown from the results, not from state.json, which may not have them.
	st := state.Load()
	for _, r := range results {
		if r.Err != nil {
			name := r.Name
			if r.Release {
				name = "ctxtra"
			}
			fmt.Fprintf(a.Out, "%s: can't check: %v\n", name, r.Err)
			n = 1
			continue
		}
		if r.Release {
			st.Ctxtra = state.Release{Check: state.Check{Checked: r.At}, Latest: r.Latest}
			fmt.Fprintf(a.Out, "ctxtra %s: %s\n", a.version, releaseStatus(cfg, st, a.version))
			continue
		}
		src, _ := cfg.Source(r.Name)
		e := st.Get(source.ID(src.Remote()), src.Remote(), src.Branch)
		e.Head, e.Checked, e.Error = r.Latest, r.At, ""
		fmt.Fprintf(a.Out, "%s: %s\n", src.Name, checkStatus(src, e))
	}
	return n
}

// lastWorked tells when the last check that worked ran, and what it found,
// after a failed one.
func lastWorked(c state.Check, found string) string {
	if c.Checked.IsZero() {
		return ""
	}
	return " (the last one that worked, " + c.Checked.Format(timeFormat) + ", found " + found + ")"
}

// timeFormat is how reports show the time of a fetch or check.
const timeFormat = "2006-01-02 15:04"

// releaseStatus says what doctor knows about ctxtra's releases.
func releaseStatus(cfg *config.Config, st state.State, version string) string {
	r := st.Ctxtra
	switch {
	case !cfg.CheckCtxtra:
		return "release check off (check-updates: false)"
	case r.Error != "":
		return "the last check for a new release failed: " + r.Error + lastWorked(r.Check, "latest release "+r.Latest)
	case r.Checked.IsZero() && !update.IsRelease(version):
		return "a " + version + " build, which sessions don't check for new releases"
	case r.Checked.IsZero():
		return "not checked for a new release yet"
	}
	if latest, command := update.Available(cfg, st, version); latest != "" {
		s := "update " + latest + " is available - " + update.Releases
		if command != "" {
			s += "; run `" + command + "` to update"
		}
		return s
	}
	checked := " (checked " + r.Checked.Format(timeFormat) + ")"
	if !update.IsRelease(version) {
		return "the latest release is " + r.Latest + checked + "; this is a " + version + " build"
	}
	return "the latest release" + checked
}

// releaseInfo is what doctor --json knows about ctxtra's releases.
func releaseInfo(cfg *config.Config, st state.State, version string) map[string]any {
	m := map[string]any{"version": version, "check_updates": cfg.CheckCtxtra}
	r := st.Ctxtra
	if r.Latest != "" {
		m["latest"] = r.Latest
	}
	if !r.Checked.IsZero() {
		m["checked"] = r.Checked.Format(time.RFC3339)
	}
	if r.Error != "" {
		m["check_error"] = r.Error
	}
	return m
}

// checkStatus says what the update checks know about the pinned source
// src, which has check-updates on.
func checkStatus(src config.Source, e state.Source) string {
	switch {
	case e.Error != "":
		found := "no new commits"
		switch e.Head {
		case src.Commit:
		case e.Dismissed:
			found = "the dismissed commit"
		default:
			found = "new commits"
		}
		return "the last check for new commits failed: " + e.Error + lastWorked(e.Check, found)
	case e.Checked.IsZero():
		return "not checked for new commits yet"
	}
	checked := " (checked " + e.Checked.Format(timeFormat) + ")"
	switch e.Head {
	case src.Commit:
		return "no new commits" + checked
	case e.Dismissed:
		return "no new commits but the dismissed one" + checked
	}
	return fmt.Sprintf("new commits%s; run `ctxtra source update %s` to review them", checked, src.Name)
}
