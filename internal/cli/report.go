package cli

import (
	"fmt"
	"os"
	"strings"

	"github.com/atelpis/ctxtra/internal/agent/registry"
	"github.com/atelpis/ctxtra/internal/config"
	"github.com/atelpis/ctxtra/internal/hook"
	"github.com/atelpis/ctxtra/internal/paths"
	"github.com/atelpis/ctxtra/internal/resolve"
	"github.com/atelpis/ctxtra/internal/source"
	"github.com/atelpis/ctxtra/internal/state"
)

// report implements the commands that show what a session gets.
type report struct{ IO }

func (r report) resolve(dir string) (*resolve.Result, *config.Config) {
	return resolve.Session(dir, paths.ConfigPath())
}

func (r report) files(dir string) int {
	res, _ := r.resolve(dir)
	for _, p := range res.Problems {
		fmt.Fprintf(r.Err, "problem: %s\n", p)
	}
	for _, h := range res.Files {
		fmt.Fprintln(r.Out, h.Path)
	}
	for _, h := range res.Skills {
		fmt.Fprintln(r.Out, h.Path)
	}
	for _, m := range res.Missing {
		fmt.Fprintf(r.Err, "missing: %s\n", m)
	}
	for _, h := range res.Dropped {
		fmt.Fprintf(r.Err, "dropped (over budget.maxChars): %s\n", h.Path)
	}
	for _, n := range res.Notices {
		fmt.Fprintf(r.Err, "notice: %s\n", n)
	}
	if len(res.Problems) > 0 || len(res.Missing) > 0 {
		return 1
	}
	return 0
}

func (r report) show(dir string) int {
	res, _ := r.resolve(dir)
	for _, p := range res.Problems {
		fmt.Fprintf(r.Err, "problem: %s\n", p)
	}
	if text := res.Render(); text != "" {
		fmt.Fprintln(r.Out, text)
	} else {
		fmt.Fprintln(r.Out, "(nothing is added here)")
	}
	return 0
}

func (r report) session(res *resolve.Result) {
	fmt.Fprintf(r.Out, "session folder: %s\n", res.Dir)
	if res.Repo == nil {
		fmt.Fprintln(r.Out, "  not in a git repository: gitRepos selectors don't match")
		return
	}
	remotes := strings.Join(res.Repo.Remotes, ", ")
	if remotes == "" {
		remotes = "(none)"
	}
	fmt.Fprintf(r.Out, "  repository: %s\n  remotes: %s\n  subpath: %s\n", res.Repo.Root, remotes, res.Repo.Subpath)
}

func (r report) explain(dir string) int {
	res, cfg := r.resolve(dir)
	fmt.Fprintf(r.Out, "config: %s\n", cfg.File)
	r.session(res)
	files := map[string]resolve.Hit{}
	for _, h := range res.Files {
		files[h.Path] = h
	}
	skills := map[string]resolve.Hit{}
	for _, h := range res.Skills {
		skills[h.Path] = h
	}
	dropped := map[string]string{}
	for _, h := range res.Dropped {
		dropped[h.Path] = h.Origin
	}
	fmt.Fprintln(r.Out)
	if len(res.Trace) == 0 {
		fmt.Fprintln(r.Out, "no bindings")
	}
	for _, t := range res.Trace {
		mark := "-"
		if t.Matched {
			mark = "+"
		}
		fmt.Fprintf(r.Out, "%s %s: %s\n", mark, t.Origin, t.Reason)
		for _, f := range t.Files {
			switch {
			case dropped[f] == t.Origin:
				fmt.Fprintf(r.Out, "    dropped  %s (over budget.maxChars)\n", f)
			case files[f].Origin == t.Origin:
				fmt.Fprintf(r.Out, "    file     %s (%d chars)\n", f, resolve.Chars(files[f].Content))
			case files[f].Origin != "" || dropped[f] != "":
				fmt.Fprintf(r.Out, "    skip     %s (already added by an earlier binding)\n", f)
			case !mentions(t.Missing, f):
				fmt.Fprintf(r.Out, "    skip     %s (not added; see problems)\n", f)
			}
		}
		for _, s := range t.Skills {
			if h, ok := skills[s]; ok && h.Origin == t.Origin {
				fmt.Fprintf(r.Out, "    skill    %s (%s)\n", s, h.Name)
			} else if !mentions(t.Missing, s) {
				fmt.Fprintf(r.Out, "    skip     %s (not added; see problems)\n", s)
			}
		}
		for _, m := range t.Missing {
			fmt.Fprintf(r.Out, "    MISSING  %s\n", m)
		}
	}
	if len(res.Problems) > 0 {
		fmt.Fprintln(r.Out, "\nproblems:")
		for _, p := range res.Problems {
			fmt.Fprintf(r.Out, "  %s\n", p)
		}
	}
	total := resolve.Chars(res.Render())
	fmt.Fprintf(r.Out, "\ntotal: %d files, %d skills, %d chars (about %d tokens)", len(res.Files), len(res.Skills), total, total/4)
	if res.MaxChars > 0 {
		fmt.Fprintf(r.Out, ", budget.maxChars %d", res.MaxChars)
	}
	fmt.Fprintln(r.Out)
	r.sizeWarnings(total, "")
	return 0
}

func (r report) sizeWarnings(total int, indent string) {
	for _, a := range registry.All() {
		if l := a.PreviewLimit(); l > 0 && total > l {
			fmt.Fprintf(r.Out, "%sWARNING: %d chars; over about %d, %s shows the model only a preview\n", indent, total, l, a.Name())
		}
	}
}

func (r report) doctor(dir string) int {
	ok := true
	res, cfg := r.resolve(dir)
	fmt.Fprintf(r.Out, "config: %s\n", cfg.File)
	if _, err := os.Stat(cfg.File); err != nil {
		fmt.Fprintln(r.Out, "  not found: nothing is added (see docs/spec.md)")
	}
	fmt.Fprintf(r.Out, "  binding files: %d, bindings: %d\n", len(cfg.BindingFiles), len(cfg.Bindings))
	for _, src := range cfg.Sources {
		fmt.Fprintf(r.Out, "  source %s: %s\n", src.Name, describeSource(src))
	}
	for _, p := range res.Problems {
		ok = false
		fmt.Fprintf(r.Out, "  PROBLEM: %s\n", p)
	}
	fmt.Fprintln(r.Out)
	r.session(res)
	for _, h := range res.Files {
		fmt.Fprintf(r.Out, "  file: %s (%d chars, %s)\n", h.Path, resolve.Chars(h.Content), h.Origin)
	}
	for _, h := range res.Skills {
		fmt.Fprintf(r.Out, "  skill: %s (%s, %s)\n", h.Name, h.Path, h.Origin)
	}
	for _, m := range res.Missing {
		ok = false
		fmt.Fprintf(r.Out, "  MISSING: %s\n", m)
	}
	for _, h := range res.Dropped {
		fmt.Fprintf(r.Out, "  DROPPED (over budget.maxChars): %s\n", h.Path)
	}
	for _, n := range res.Notices {
		fmt.Fprintf(r.Out, "  NOTICE: %s\n", n)
	}
	r.sizeWarnings(resolve.Chars(res.Render()), "  ")

	fmt.Fprintln(r.Out, "\nhooks:")
	for _, a := range registry.All() {
		st := a.Status()
		ok = ok && st.OK
		fmt.Fprintf(r.Out, "  %s: %s  (%s)\n", a.Name(), st.State, a.ConfigPath())
		for _, d := range st.Details {
			fmt.Fprintf(r.Out, "    %s\n", d)
		}
	}

	repo := ""
	if res.Repo != nil {
		repo = res.Repo.Root
	}
	fmt.Fprintln(r.Out, "\nlast hook runs here:")
	runs := hook.LastRuns(res.Dir, repo)
	if len(runs) == 0 {
		fmt.Fprintln(r.Out, "  none yet")
	}
	for _, e := range runs {
		status := fmt.Sprintf("files=%d skills=%d missing=%d", len(e.Files), len(e.Skills), len(e.Missing))
		if e.Error != "" {
			status = "error: " + e.Error
		}
		fmt.Fprintf(r.Out, "  %s %s: %s %s\n", e.Harness, e.Event, e.Ts, status)
	}
	if ok {
		return 0
	}
	return 1
}

// mentions reports whether any entry of list starts with s.
func mentions(list []string, s string) bool {
	for _, x := range list {
		if strings.HasPrefix(x, s) {
			return true
		}
	}
	return false
}

// describeSource is one line about a source and its state, for doctor.
func describeSource(s config.Source) string {
	if !s.Pinned() {
		state := ""
		if _, err := os.Stat(s.TrustedPath); err != nil {
			state = " (not found)"
		}
		return "trusted-path " + paths.Tilde(s.TrustedPath) + state
	}
	d := fmt.Sprintf("git %s, branch %s, ", s.Git, s.Branch)
	switch {
	case s.Commit == "":
		return d + "no accepted commit yet (run: ctxtra sync " + s.Name + ")"
	case !source.Has(s.Git, s.Commit):
		return d + "pinned to " + source.Short(s.Commit) + ", NOT IN THE STORE (run: ctxtra sync --restore)"
	}
	d += "pinned to " + source.Short(s.Commit)
	e := state.Load().Get(source.ID(s.Git), s.Git, s.Branch)
	if e.LastCheck.IsZero() {
		d += ", not checked for new commits on this machine yet"
	} else {
		d += ", last checked " + e.LastCheck.Format("2006-01-02 15:04")
	}
	if e.Dismissed != "" && e.Dismissed != s.Commit {
		d += ", " + source.Short(e.Dismissed) + " not accepted (dismissed)"
	}
	return d
}
