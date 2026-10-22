package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/atelpis/ctxtra/internal/agent"
	"github.com/atelpis/ctxtra/internal/agent/registry"
	"github.com/atelpis/ctxtra/internal/chunk"
	"github.com/atelpis/ctxtra/internal/config"
	"github.com/atelpis/ctxtra/internal/hook"
	"github.com/atelpis/ctxtra/internal/paths"
	"github.com/atelpis/ctxtra/internal/resolve"
	"github.com/atelpis/ctxtra/internal/source"
	"github.com/atelpis/ctxtra/internal/state"
	"github.com/atelpis/ctxtra/internal/term"
)

// report implements the commands that show what a session gets.
type report struct{ IO }

func (r report) resolve(dir string) (*resolve.Result, *config.Config) {
	return resolve.Session(dir, paths.ConfigPath())
}

// status is the exit code of a command that shows res: 1 when a session
// wouldn't get all it should.
func status(res *resolve.Result) int {
	if len(res.Problems) > 0 || len(res.Missing) > 0 {
		return 1
	}
	return 0
}

// sessionDiagnostics are res's problems, missing files, drops and notices,
// for --json.
func sessionDiagnostics(res *resolve.Result) []diagnostic {
	d := diagnostics("error", res.Problems)
	for _, m := range res.Missing {
		d = append(d, diagnostic{"error", "missing: " + m})
	}
	for _, h := range res.Dropped {
		d = append(d, diagnostic{"warning", fmt.Sprintf("dropped (over %s): %s", h.Over, h.Path)})
	}
	return append(d, diagnostics("notice", res.Notices)...)
}

// hitInfo is a file or skill a session gets, in --json output.
type hitInfo struct {
	Path    string `json:"path"`
	Name    string `json:"name,omitempty"` // skills
	Binding string `json:"binding"`
	Chars   int    `json:"chars,omitempty"` // files
}

func hits(hs []resolve.Hit, skills bool) []hitInfo {
	out := []hitInfo{}
	for _, h := range hs {
		i := hitInfo{Path: h.Path, Binding: h.Origin}
		if skills {
			i.Name = h.Name
		} else {
			i.Chars = resolve.Chars(h.Content)
		}
		out = append(out, i)
	}
	return out
}

func (r report) files(dir string, asJSON bool) int {
	res, _ := r.resolve(dir)
	if asJSON {
		writeJSON(r.Out, map[string]any{"dir": res.Dir, "files": hits(res.Files, false), "skills": hits(res.Skills, true)},
			sessionDiagnostics(res))
		return status(res)
	}
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
		fmt.Fprintf(r.Err, "dropped (over %s): %s\n", h.Over, h.Path)
	}
	for _, n := range res.Notices {
		fmt.Fprintf(r.Err, "notice: %s\n", n)
	}
	return status(res)
}

// showPart is the most bytes that context show --part prints. Agents cut a
// long command output: Codex shows the model about 10,000 bytes of it.
const showPart = 8000

// showTitle is the most bytes of a block's title in context show --part.
const showTitle = 1000

// showFooter ends a part of context show --part that isn't the last one.
const showFooter = "\n[ctxtra: part %d of %d; for the next part, run the same command with --part %d]\n"

type showOptions struct {
	exclude []string // leave out what sessions in these folders get
	part    int      // show only this part of the text, from 1; 0 for all of it
}

func (r report) show(dir string, o showOptions) int {
	res, _ := r.resolve(dir)
	for _, p := range res.Problems {
		fmt.Fprintf(r.Err, "problem: %s\n", p)
	}
	for _, m := range res.Missing {
		fmt.Fprintf(r.Err, "missing: %s\n", m)
	}
	empty := "(nothing is added here)"
	if len(o.exclude) > 0 {
		// A file the session has from another folder is listed, without its
		// text: its header there doesn't say that it applies here too.
		for _, s := range o.exclude {
			base, _ := r.resolve(s)
			// Only the files that every agent's session gets in full: the
			// command doesn't know which agent runs it.
			base.Fit(func(c *resolve.Result) bool {
				return !slices.ContainsFunc(registry.All(), func(a agent.Agent) bool { return !a.Delivery().Fits(c) })
			}, "")
			res.Files = slices.DeleteFunc(res.Files, func(h resolve.Hit) bool {
				i := slices.IndexFunc(base.Files, func(b resolve.Hit) bool { return b.Path == h.Path })
				if i < 0 {
					return false
				}
				if !h.Global || !base.Files[i].Global {
					res.Known = append(res.Known, h)
				}
				return true
			})
			res.Skills = slices.DeleteFunc(res.Skills, func(h resolve.Hit) bool {
				return slices.ContainsFunc(base.Skills, func(b resolve.Hit) bool { return b.Path == h.Path })
			})
		}
		empty = "(nothing is added here beyond what sessions in the --exclude-session folders get)"
	}
	if o.part == 0 {
		if text := res.Render(); text != "" {
			fmt.Fprintln(r.Out, text)
		} else {
			fmt.Fprintln(r.Out, empty)
		}
		return status(res)
	}
	// Measured as printed: the output escapes control characters. Room is
	// kept for the line end and the footer.
	// A title repeats in the header of each continued part: a long one, such
	// as a path of escapes, is cut so that the parts keep room for the text.
	blocks := res.Blocks()
	for i, b := range blocks {
		blocks[i] = chunk.Block{Title: cut(term.Sanitize(b.Title), showTitle), Text: term.Sanitize(b.Text)}
	}
	room := 1 + len(fmt.Sprintf(showFooter, 99999, 99999, 99999))
	parts := chunk.Split(blocks, showPart-room, chunk.Bytes)
	switch {
	case len(parts) == 0 && o.part == 1:
		fmt.Fprintln(r.Out, empty)
		return status(res)
	case o.part < 1 || o.part > len(parts):
		fmt.Fprintf(r.Err, "ctxtra context show: --part %d: the text has %d parts\n", o.part, len(parts))
		return 2
	}
	fmt.Fprintln(r.Out, parts[o.part-1])
	if o.part < len(parts) {
		fmt.Fprintf(r.Out, showFooter, o.part, len(parts), o.part+1)
	}
	return status(res)
}

// cut is s cut to at most n bytes, at the start of a character, with "..."
// at the end when it is cut.
func cut(s string, n int) string {
	if len(s) <= n {
		return s
	}
	n -= len("...")
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n] + "..."
}

// sessionInfo is the session folder in --json output.
type sessionInfo struct {
	Dir        string   `json:"dir"`
	Repository string   `json:"repository,omitempty"`
	Remotes    []string `json:"remotes,omitempty"`
	Subpath    string   `json:"subpath,omitempty"`
}

func session(res *resolve.Result) sessionInfo {
	s := sessionInfo{Dir: res.Dir}
	if res.Repo != nil {
		s.Repository, s.Remotes, s.Subpath = res.Repo.Root, res.Repo.Remotes, res.Repo.Subpath
	}
	return s
}

func (r report) session(res *resolve.Result) {
	fmt.Fprintf(r.Out, "session folder: %s\n", res.Dir)
	if res.Repo == nil {
		fmt.Fprintln(r.Out, "  not in a git repository: repos in match blocks don't match")
		return
	}
	remotes := strings.Join(res.Repo.Remotes, ", ")
	if remotes == "" {
		remotes = "(none)"
	}
	fmt.Fprintf(r.Out, "  repository: %s\n  remotes: %s\n  subpath: %s\n", res.Repo.Root, remotes, res.Repo.Subpath)
}

// traceItem is a file or skill of a binding in explain --json: added,
// dropped, skipped (an earlier binding added it, or a problem), or missing.
type traceItem struct {
	Path   string `json:"path"`
	Status string `json:"status"`
	Chars  int    `json:"chars,omitempty"`
	Name   string `json:"name,omitempty"` // skills
	Over   string `json:"over,omitempty"` // dropped files: the limit
}

// traceInfo is a binding in explain --json.
type traceInfo struct {
	Binding string      `json:"binding"`
	Matched bool        `json:"matched"`
	Reason  string      `json:"reason"`
	Files   []traceItem `json:"files"`
	Skills  []traceItem `json:"skills"`
	Missing []string    `json:"missing"`
}

// agentDrop is an agent whose sessions don't get all of a text, in --json.
type agentDrop struct {
	Agent   string   `json:"agent"`
	Over    string   `json:"over"`
	Dropped []string `json:"dropped"`
}

// traces is how each binding of res added its files and skills.
func traces(res *resolve.Result) []traceInfo {
	files := map[string]resolve.Hit{}
	for _, h := range res.Files {
		files[h.Path] = h
	}
	skills := map[string]resolve.Hit{}
	for _, h := range res.Skills {
		skills[h.Path] = h
	}
	dropped := map[string]resolve.Hit{}
	for _, h := range res.Dropped {
		dropped[h.Path] = h
	}
	out := []traceInfo{}
	for _, t := range res.Trace {
		ti := traceInfo{Binding: t.Origin, Matched: t.Matched, Reason: t.Reason, Files: []traceItem{}, Skills: []traceItem{},
			Missing: append([]string{}, t.Missing...)}
		for _, f := range t.Files {
			switch {
			case dropped[f].Origin == t.Origin:
				ti.Files = append(ti.Files, traceItem{Path: f, Status: "dropped", Over: dropped[f].Over})
			case files[f].Origin == t.Origin:
				ti.Files = append(ti.Files, traceItem{Path: f, Status: "added", Chars: resolve.Chars(files[f].Content)})
			case files[f].Origin != "" || dropped[f].Origin != "":
				ti.Files = append(ti.Files, traceItem{Path: f, Status: "skipped: already added by an earlier binding"})
			case !mentions(t.Missing, f):
				ti.Files = append(ti.Files, traceItem{Path: f, Status: "skipped: not added; see problems"})
			}
		}
		for _, s := range t.Skills {
			if h, ok := skills[s]; ok && h.Origin == t.Origin {
				ti.Skills = append(ti.Skills, traceItem{Path: s, Status: "added", Name: h.Name})
			} else if !mentions(t.Missing, s) {
				ti.Skills = append(ti.Skills, traceItem{Path: s, Status: "skipped: not added; see problems"})
			}
		}
		out = append(out, ti)
	}
	return out
}

// agentDrops are the agents that wouldn't get all of res, with the files
// their sessions don't get.
func agentDrops(res *resolve.Result) []agentDrop {
	out := []agentDrop{}
	for _, a := range registry.All() {
		d := a.Delivery()
		c := *res
		c.Dropped = nil
		d.Fit(&c)
		if len(c.Dropped) == 0 {
			continue
		}
		names := make([]string, len(c.Dropped))
		for i, h := range c.Dropped {
			names[i] = filepath.Base(h.Path)
		}
		out = append(out, agentDrop{a.Name(), d.Over(), names})
	}
	return out
}

func (r report) explain(dir string, asJSON bool) int {
	res, cfg := r.resolve(dir)
	total := resolve.Chars(res.Render())
	if asJSON {
		writeJSON(r.Out, map[string]any{
			"config": cfg.File, "session": session(res), "bindings": traces(res),
			"total":     map[string]int{"files": len(res.Files), "skills": len(res.Skills), "chars": total, "tokens": total / 4},
			"max_chars": res.MaxChars, "agents_over": agentDrops(res),
		}, sessionDiagnostics(res))
		return status(res)
	}
	fmt.Fprintf(r.Out, "config: %s\n", cfg.File)
	r.session(res)
	fmt.Fprintln(r.Out)
	if len(res.Trace) == 0 {
		fmt.Fprintln(r.Out, "no bindings")
	}
	for _, t := range traces(res) {
		mark := "-"
		if t.Matched {
			mark = "+"
		}
		fmt.Fprintf(r.Out, "%s %s: %s\n", mark, t.Binding, t.Reason)
		for _, f := range t.Files {
			switch f.Status {
			case "dropped":
				fmt.Fprintf(r.Out, "    dropped  %s (over %s)\n", f.Path, f.Over)
			case "added":
				fmt.Fprintf(r.Out, "    file     %s (%d chars)\n", f.Path, f.Chars)
			default:
				fmt.Fprintf(r.Out, "    skip     %s (%s)\n", f.Path, strings.TrimPrefix(f.Status, "skipped: "))
			}
		}
		for _, s := range t.Skills {
			if s.Status == "added" {
				fmt.Fprintf(r.Out, "    skill    %s (%s)\n", s.Path, s.Name)
			} else {
				fmt.Fprintf(r.Out, "    skip     %s (%s)\n", s.Path, strings.TrimPrefix(s.Status, "skipped: "))
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
	fmt.Fprintf(r.Out, "\ntotal: %d files, %d skills, %d chars (about %d tokens)", len(res.Files), len(res.Skills), total, total/4)
	if res.MaxChars > 0 {
		fmt.Fprintf(r.Out, ", budget.max-chars %d", res.MaxChars)
	}
	fmt.Fprintln(r.Out)
	r.sizeWarnings(res, "")
	return status(res)
}

// sizeWarnings warns about each agent that wouldn't get all of res: the
// files its sessions don't get.
func (r report) sizeWarnings(res *resolve.Result, indent string) {
	for _, d := range agentDrops(res) {
		fmt.Fprintf(r.Out, "%swarning: over %s, %s sessions don't get %s\n", indent, d.Over, d.Agent, agent.Names(d.Dropped))
	}
}

// hookInfo is an agent's setup in doctor --json.
type hookInfo struct {
	Agent   string   `json:"agent"`
	State   string   `json:"state"`
	OK      bool     `json:"ok"`
	Config  string   `json:"config"`
	Details []string `json:"details"`
}

func (r report) doctor(dir string, asJSON bool) int {
	ok := true
	res, cfg := r.resolve(dir)
	_, statErr := os.Stat(cfg.File)
	setups, _, _ := state.LoadSetups()
	bin := running()
	var agents []hookInfo
	for _, a := range registry.All() {
		st := a.Status(bin, setups.Known(a.Name(), a.ConfigPath()))
		ok = ok && st.OK
		agents = append(agents, hookInfo{a.Name(), st.State, st.OK, a.ConfigPath(), append([]string{}, st.Details...)})
	}
	repo := ""
	if res.Repo != nil {
		repo = res.Repo.Root
	}
	runs := hook.LastRuns(res.Dir, repo)
	ok = ok && status(res) == 0
	if asJSON {
		sources := []sourceInfo{}
		for _, src := range cfg.Sources {
			sources = append(sources, info(src))
		}
		if runs == nil {
			runs = []hook.Entry{}
		}
		writeJSON(r.Out, map[string]any{
			"config":  map[string]any{"file": cfg.File, "exists": statErr == nil, "binding_files": len(cfg.BindingFiles), "bindings": len(cfg.Bindings)},
			"sources": sources, "session": session(res),
			"files": hits(res.Files, false), "skills": hits(res.Skills, true), "agents_over": agentDrops(res),
			"hooks": agents, "last_hook_runs": runs,
		}, append(sessionDiagnostics(res), diagnostics("notice", cfg.Notes)...))
		if ok {
			return 0
		}
		return 1
	}
	fmt.Fprintf(r.Out, "config: %s\n", cfg.File)
	if statErr != nil {
		fmt.Fprintln(r.Out, "  not found: nothing is added (the format: ctxtra help config)")
	}
	fmt.Fprintf(r.Out, "  binding files: %d, bindings: %d\n", len(cfg.BindingFiles), len(cfg.Bindings))
	for _, src := range cfg.Sources {
		fmt.Fprintf(r.Out, "  source %s: %s\n", src.Name, describeSource(src))
	}
	for _, n := range cfg.Notes {
		fmt.Fprintf(r.Out, "  NOTE: %s\n", n)
	}
	for _, p := range res.Problems {
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
		fmt.Fprintf(r.Out, "  MISSING: %s\n", m)
	}
	for _, h := range res.Dropped {
		fmt.Fprintf(r.Out, "  DROPPED (over %s): %s\n", h.Over, h.Path)
	}
	for _, n := range res.Notices {
		fmt.Fprintf(r.Out, "  NOTICE: %s\n", n)
	}
	r.sizeWarnings(res, "  ")

	fmt.Fprintln(r.Out, "\nhooks:")
	for _, a := range agents {
		fmt.Fprintf(r.Out, "  %s: %s  (%s)\n", a.Agent, a.State, a.Config)
		for _, d := range a.Details {
			fmt.Fprintf(r.Out, "    %s\n", d)
		}
	}
	fmt.Fprintln(r.Out, "\nlast hook runs here:")
	if len(runs) == 0 {
		fmt.Fprintln(r.Out, "  none yet")
	}
	for _, e := range runs {
		status := fmt.Sprintf("files=%d skills=%d missing=%d", len(e.Files), len(e.Skills), len(e.Missing))
		if e.Error != "" {
			status = "error: " + e.Error
		}
		if e.Skipped != "" {
			status = "skipped: " + e.Skipped
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

// describeSource is one line about a source and its state.
func describeSource(s config.Source) string {
	if !s.Pinned() {
		state := ""
		if _, err := os.Stat(s.TrustedPath); err != nil {
			state = " (not found)"
		}
		return "trusted-path " + paths.Tilde(s.TrustedPath) + state + ": read from disk, changes apply without review"
	}
	d := fmt.Sprintf("git %s, branch %s, ", s.Git, s.Branch)
	if s.LocalGit != "" {
		d = "local-git " + paths.Tilde(s.LocalGit) + ", follows its committed HEAD, "
	}
	switch {
	case s.Commit == "":
		return d + "no accepted commit yet (accept one in a terminal: ctxtra source update " + s.Name + ")"
	case !source.Has(s.Remote(), s.Commit):
		return d + "pinned to " + source.Short(s.Commit) + ", NOT IN THE STORE (run: ctxtra source restore " + s.Name + ")"
	}
	d += "pinned to " + source.Short(s.Commit) + " (new commits apply after you accept them in ctxtra source update)"
	e := state.Load().Get(source.ID(s.Remote()), s.Remote(), s.Branch)
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
