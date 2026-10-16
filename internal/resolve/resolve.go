// Package resolve works out what a session gets: which bindings match the
// session folder, and the files and skills they add.
package resolve

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"unicode/utf8"

	"github.com/atelpis/ctxtra/internal/config"
	"github.com/atelpis/ctxtra/internal/fsutil"
	"github.com/atelpis/ctxtra/internal/gitrepo"
	"github.com/atelpis/ctxtra/internal/paths"
)

// Hit is a file or a skill added to the session.
type Hit struct {
	Path    string // the file, or the skill's SKILL.md
	Content string // a file's text; a skill's SKILL.md text after the frontmatter
	Origin  string // the binding that added it
	Global  bool   // from a binding with `global: true`
	// Skills only.
	Name, Description string
}

// Trace explains one binding, for `explain`.
type Trace struct {
	Origin   string
	Matched  bool
	Reason   string
	Files    []string
	Skills   []string // SKILL.md paths
	Missing  []string
	Problems []string
}

// Result is what a session in Dir gets.
type Result struct {
	Dir      string
	Repo     *gitrepo.Repo // nil outside git
	Files    []Hit
	Skills   []Hit
	Dropped  []Hit // over budget.maxChars
	Missing  []string
	Problems []string
	Trace    []Trace
	MaxChars int
}

// Session loads the config at configPath and resolves it for a session in
// dir. All reads share the per-session limits in config.
func Session(dir, configPath string) (*Result, *config.Config) {
	rd := fsutil.NewReader(config.MaxFileBytes, config.MaxSessionBytes)
	cfg := config.Load(configPath, rd)
	return Resolve(dir, cfg, rd), cfg
}

// Resolve works out what a session started in dir gets, reading files with
// rd. Bindings apply in config order; a file that several bindings add is
// added once, at its first position, and the same for skills.
func Resolve(dir string, cfg *config.Config, rd config.Reader) *Result {
	dir = paths.Real(dir)
	res := &Result{Dir: dir, Repo: gitrepo.Identify(dir), MaxChars: cfg.MaxChars}
	res.Problems = append(res.Problems, cfg.Problems...)
	l := &loader{rd: rd, cfg: cfg, files: map[string]bool{}, skills: map[string]bool{}}
	for _, b := range cfg.Bindings {
		t := Trace{Origin: b.ID()}
		t.Matched, t.Reason = b.Selector.Match(dir, res.Repo)
		if t.Matched {
			res.apply(&t, b, l)
		}
		res.Trace = append(res.Trace, t)
	}
	res.dedupeSkillNames()
	res.applyBudget()
	return res
}

func (r *Result) apply(t *Trace, b config.Binding, l *loader) {
	for _, raw := range b.Files {
		f := paths.Real(raw)
		t.Files = append(t.Files, f)
		if content, ok := l.load(t, raw, f, l.files); ok {
			r.Files = append(r.Files, Hit{Path: f, Content: content, Origin: t.Origin, Global: b.Selector.Global})
		}
	}
	for _, dir := range b.Skills {
		raw := filepath.Join(dir, "SKILL.md")
		f := paths.Real(raw)
		t.Skills = append(t.Skills, f)
		content, ok := l.load(t, raw, f, l.skills)
		if !ok {
			continue
		}
		name, desc, body, err := parseSkill(content)
		if err != nil {
			t.Problems = append(t.Problems, fmt.Sprintf("%s: %v; skipped", f, err))
			continue
		}
		if name == "" {
			name = filepath.Base(filepath.Dir(f))
		}
		r.Skills = append(r.Skills, Hit{Path: f, Origin: t.Origin, Global: b.Selector.Global, Name: name, Description: desc,
			Content: body})
	}
	r.Missing = append(r.Missing, t.Missing...)
	for _, p := range t.Problems {
		r.Problems = append(r.Problems, t.Origin+": "+p)
	}
}

// loader adds each file and each skill once. Files and skills are tracked
// apart: the same SKILL.md can be both an instruction file and a skill.
type loader struct {
	rd            config.Reader
	cfg           *config.Config
	files, skills map[string]bool
}

// load reads path, the real path of raw, unless added already to seen. raw
// is checked with the config's trust policy first (the check needs the
// symlinks on the way), unless it is in a pinned source's store folder. A
// missing file goes to t.Missing, other errors to t.Problems.
func (l *loader) load(t *Trace, raw, path string, seen map[string]bool) (string, bool) {
	if seen[path] {
		return "", false
	}
	if !l.cfg.Pinned(raw) {
		if err := l.cfg.Trust.Check(raw); err != nil {
			t.Problems = append(t.Problems, err.Error())
			return "", false
		}
	}
	b, err := l.rd.Read(path)
	switch {
	case errors.Is(err, os.ErrNotExist):
		t.Missing = append(t.Missing, path)
		return "", false
	case err != nil:
		t.Problems = append(t.Problems, fmt.Sprintf("%v; skipped", err))
		return "", false
	}
	seen[path] = true
	return string(b), true
}

// dedupeSkillNames keeps the first skill of each name: agents pick skills by
// name, so two with the same name would be ambiguous.
func (r *Result) dedupeSkillNames() {
	first := map[string]Hit{}
	var kept []Hit
	for _, h := range r.Skills {
		if f, ok := first[h.Name]; ok {
			r.Problems = append(r.Problems, fmt.Sprintf("%s: skill %q is already added from %s; skipped", h.Origin, h.Name, f.Path))
			continue
		}
		first[h.Name] = h
		kept = append(kept, h)
	}
	r.Skills = kept
}

// Chars is the length of s in characters (Unicode code points), the unit of
// budget.maxChars.
func Chars(s string) int { return utf8.RuneCountInString(s) }

// applyBudget drops whole files from the end until the text fits
// budget.maxChars. The skill list is never dropped.
func (r *Result) applyBudget() {
	total := Chars(r.Render())
	if r.MaxChars <= 0 || total <= r.MaxChars {
		return
	}
	n := len(r.Files)
	for n > 0 && total > r.MaxChars {
		n--
		total -= Chars(r.block(r.Files[n])) + Chars(blockSep)
	}
	r.Dropped = append(r.Dropped, r.Files[n:]...)
	r.Files = r.Files[:n]
}
