package resolve

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/atelpis/ctxtra/internal/config"
	"github.com/atelpis/ctxtra/internal/fsutil"
	"github.com/atelpis/ctxtra/internal/source"
	"github.com/atelpis/ctxtra/internal/state"
	"github.com/atelpis/ctxtra/internal/testenv"
)

const head = "schema: ctxtra/v1\nbindings:\n"

func load(t *testing.T, h, bindings, budget string) *config.Config {
	t.Helper()
	testenv.Write(t, filepath.Join(h, ".config", "ctxtra", "b.yaml"), bindings)
	return config.Load(testenv.Config(t, h, "schema: ctxtra/v1\ninclude: [b.yaml]\n"+budget),
		fsutil.NewReader(config.MaxFileBytes, config.MaxSessionBytes))
}

func run(dir string, cfg *config.Config) *Result {
	return Resolve(dir, cfg, fsutil.NewReader(config.MaxFileBytes, config.MaxSessionBytes))
}

func names(hits []Hit) []string {
	var out []string
	for _, h := range hits {
		out = append(out, filepath.Base(h.Path))
	}
	return out
}

func TestOrderDedupeAndMissing(t *testing.T) {
	h := testenv.Home(t)
	r := testenv.Repo(t, filepath.Join(h, "svc"), "git@github.com:acme/svc.git")
	for _, n := range []string{"a", "b", "c"} {
		testenv.Write(t, filepath.Join(h, "n", n+".md"), strings.ToUpper(n))
	}
	cfg := load(t, h, head+`  - {name: one, match: {global: true}, instructions: [~/n/b.md, ~/n/a.md]}
  - {name: two, match: {repos: [github.com/acme/svc]}, instructions: [~/n/a.md, ~/n/c.md, ~/n/gone.md]}
  - {name: three, match: {repos: [{remote: github.com/other/x}]}, instructions: [~/n/c.md]}
`, "")
	res := run(r, cfg)
	if got := names(res.Files); !slices.Equal(got, []string{"b.md", "a.md", "c.md"}) {
		t.Errorf("files %v", got)
	}
	if len(res.Missing) != 1 || !strings.HasSuffix(res.Missing[0], "gone.md") {
		t.Errorf("missing %v", res.Missing)
	}
	if len(res.Trace) != 3 || res.Trace[2].Matched {
		t.Errorf("trace %+v", res.Trace)
	}
	// Outside git only the global binding applies.
	if got := names(run(h, cfg).Files); !slices.Equal(got, []string{"b.md", "a.md"}) {
		t.Errorf("outside git: %v", got)
	}
}

func TestRender(t *testing.T) {
	h := testenv.Home(t)
	r := testenv.Repo(t, filepath.Join(h, "svc"), "git@github.com:acme/svc.git")
	plain := filepath.Join(h, "plain")
	if err := os.MkdirAll(plain, 0o755); err != nil {
		t.Fatal(err)
	}
	testenv.Write(t, filepath.Join(h, "g.md"), "G\n\n")
	testenv.Write(t, filepath.Join(h, "s.md"), "S")
	cfg := load(t, h, head+"  - {name: g, match: {global: true}, instructions: [~/g.md]}\n  - {name: s, match: {dirs: [~/**]}, instructions: [~/s.md]}\n", "")
	text := run(r, cfg).Render()
	for _, want := range []string{
		"Contents of " + filepath.Join(h, "g.md") + " (instructions added by ctxtra for every session; binding: g (~/.config/ctxtra/b.yaml)):\n\nG\n\n",
		"for this repository, ~/svc, and all its worktrees",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("missing %q in:\n%s", want, text)
		}
	}
	if text := run(plain, cfg).Render(); !strings.Contains(text, "for sessions in ~/plain") {
		t.Errorf("plain folder:\n%s", text)
	}
}

func TestBudgetDropsFromTheEnd(t *testing.T) {
	h := testenv.Home(t)
	testenv.Skill(t, filepath.Join(h, "s", "deploy"), "description: Deploy it.")
	// Two bytes per character: the budget counts characters.
	for _, n := range []string{"a", "b", "c"} {
		testenv.Write(t, filepath.Join(h, n+".md"), strings.Repeat("é", 500))
	}
	cfg := load(t, h, head+"  - {name: x, match: {global: true}, instructions: [~/a.md, ~/b.md, ~/c.md], skills: [~/s/deploy]}\n", "budget: {max-chars: 1500}\n")
	res := run(h, cfg)
	if got := names(res.Files); !slices.Equal(got, []string{"a.md"}) {
		t.Errorf("kept %v, dropped %v, %d chars", got, names(res.Dropped), Chars(res.Render()))
	}
	if len(res.Dropped) != 2 || res.Dropped[0].Over != "budget.max-chars" {
		t.Errorf("dropped %+v", res.Dropped)
	}
	if len(res.Skills) != 1 || Chars(res.Render()) > 1500 || len(res.Render()) <= 1500 {
		t.Errorf("skills %v, %d chars, %d bytes", names(res.Skills), Chars(res.Render()), len(res.Render()))
	}
}

// Fit drops files from the end, before the ones dropped already, and the
// text it keeps is the same as without them.
func TestFit(t *testing.T) {
	res := &Result{Dir: "/x", Skills: []Hit{{Name: "s", Description: "d", Path: "/s/SKILL.md"}},
		Dropped: []Hit{{Path: "/n/e.md", Over: "budget.max-chars"}}}
	for _, n := range []string{"a", "b", "c", "d"} {
		res.Files = append(res.Files, Hit{Path: "/n/" + n + ".md", Content: strings.Repeat(n, 100), Global: true})
	}
	calls := 0
	res.Fit(func(r *Result) bool { calls++; return len(r.Files) <= 1 }, "Codex's limit")
	if got := names(res.Files); !slices.Equal(got, []string{"a.md"}) || calls > 4 {
		t.Errorf("kept %v after %d calls", got, calls)
	}
	var over []string
	for _, h := range res.Dropped {
		over = append(over, filepath.Base(h.Path)+": "+h.Over)
	}
	if want := []string{"b.md: Codex's limit", "c.md: Codex's limit", "d.md: Codex's limit", "e.md: budget.max-chars"}; !slices.Equal(over, want) {
		t.Errorf("dropped %v", over)
	}
	if text := res.Render(); !strings.Contains(text, "aaa") || strings.Contains(text, "bbb") || !strings.Contains(text, "<name>s</name>") {
		t.Errorf("text:\n%s", text)
	}
	// Nothing fits: every file is dropped, the skill list stays.
	res.Fit(func(r *Result) bool { return false }, "x")
	if len(res.Files) != 0 || len(res.Dropped) != 5 || res.Render() == "" {
		t.Errorf("files %v, dropped %v", names(res.Files), names(res.Dropped))
	}
}

func TestSkills(t *testing.T) {
	h := testenv.Home(t)
	testenv.Skill(t, filepath.Join(h, "s", "deploy"), "name: deploy\ndescription: |\n  Deploy\n  it <now>.\nallowed-tools: x")
	testenv.Skill(t, filepath.Join(h, "s", "unnamed"), "description: Uses the folder name.")
	testenv.Skill(t, filepath.Join(h, "t", "deploy"), "description: Same name as the first.")
	testenv.Skill(t, filepath.Join(h, "s", "nodesc"), "name: nodesc")
	testenv.Write(t, filepath.Join(h, "s", "nofront", "SKILL.md"), "# no frontmatter\n")
	testenv.Skill(t, filepath.Join(h, "s", "long"), "description: "+strings.Repeat("é", 600))
	cfg := load(t, h, head+`  - name: x
    match: {global: true}
    skills: [~/s/deploy, ~/s/unnamed, ~/t/deploy, ~/s/nodesc, ~/s/nofront, ~/s/long, ~/s/missing]
`, "")
	res := run(h, cfg)
	var got []string
	for _, s := range res.Skills {
		got = append(got, s.Name)
	}
	if !slices.Equal(got, []string{"deploy", "unnamed", "long"}) {
		t.Errorf("skills %v, problems %v", got, res.Problems)
	}
	if res.Skills[0].Description != "Deploy it <now>." || len(res.Problems) != 3 || len(res.Missing) != 1 {
		t.Errorf("description %q, problems %v, missing %v", res.Skills[0].Description, res.Problems, res.Missing)
	}
	if d := res.Skills[2].Description; !strings.HasSuffix(d, "…") || !strings.HasPrefix(d, "é") || len(d) > maxSkillDesc {
		t.Errorf("long description %d bytes", len(d))
	}
	if text := res.Render(); !strings.Contains(text, "<description>Deploy it &lt;now&gt;.</description>") {
		t.Errorf("skill list:\n%s", text)
	}
}

func TestLoadLimits(t *testing.T) {
	h := testenv.Home(t)
	big := strings.Repeat("x", config.MaxFileBytes+1)
	testenv.Write(t, filepath.Join(h, "big.md"), big)
	if err := os.Mkdir(filepath.Join(h, "dir.md"), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := load(t, h, head+"  - {name: x, match: {global: true}, instructions: [~/big.md, ~/dir.md]}\n", "")
	res := run(h, cfg)
	if len(res.Files) != 0 || len(res.Problems) != 2 {
		t.Errorf("files %v problems %v", names(res.Files), res.Problems)
	}
}

// The total read limit covers the config and binding files too.
func TestTotalLimitCoversConfig(t *testing.T) {
	h := testenv.Home(t)
	testenv.Write(t, filepath.Join(h, "a.md"), "A")
	testenv.Write(t, filepath.Join(h, "b.md"), strings.Repeat("B", 10))
	testenv.Write(t, filepath.Join(h, ".config", "ctxtra", "b.yaml"),
		head+"  - {name: x, match: {global: true}, instructions: [~/a.md, ~/b.md]}\n")
	cfgPath := testenv.Config(t, h, "schema: ctxtra/v1\ninclude: [b.yaml]\n")
	size := func(p string) int {
		st, err := os.Stat(p)
		if err != nil {
			t.Fatal(err)
		}
		return int(st.Size())
	}
	total := size(cfgPath) + size(filepath.Join(h, ".config", "ctxtra", "b.yaml")) + 1 + 5
	rd := fsutil.NewReader(config.MaxFileBytes, total)
	res := Resolve(h, config.Load(cfgPath, rd), rd)
	if got := names(res.Files); !slices.Equal(got, []string{"a.md"}) || len(res.Problems) != 1 ||
		!strings.Contains(res.Problems[0], "total read limit") {
		t.Errorf("files %v, problems %v", got, res.Problems)
	}
}

// The same SKILL.md can be an instruction file and a skill at once.
func TestFileAndSkillAreDedupedApart(t *testing.T) {
	h := testenv.Home(t)
	testenv.Skill(t, filepath.Join(h, "s", "deploy"), "description: Deploy it.")
	cfg := load(t, h, head+"  - {name: x, match: {global: true}, instructions: [~/s/deploy/SKILL.md], skills: [~/s/deploy]}\n", "")
	if res := run(h, cfg); len(res.Files) != 1 || len(res.Skills) != 1 {
		t.Errorf("files %v, skills %v, problems %v", names(res.Files), names(res.Skills), res.Problems)
	}
}

// Without git (or when git doesn't answer), repos bindings don't match
// but every other binding still applies.
func TestWithoutGit(t *testing.T) {
	h := testenv.Home(t)
	r := testenv.Repo(t, filepath.Join(h, "svc"), "git@github.com:acme/svc.git")
	testenv.Write(t, filepath.Join(h, "a.md"), "A")
	testenv.Write(t, filepath.Join(h, "b.md"), "B")
	cfg := load(t, h, head+"  - {name: g, match: {global: true}, instructions: [~/a.md]}\n  - {name: r, match: {repos: [{remote: github.com/acme/svc}]}, instructions: [~/b.md]}\n", "")
	if got := names(run(r, cfg).Files); !slices.Equal(got, []string{"a.md", "b.md"}) {
		t.Fatalf("with git: %v", got)
	}
	t.Setenv("PATH", t.TempDir())
	if got := names(run(r, cfg).Files); !slices.Equal(got, []string{"a.md"}) {
		t.Errorf("without git: %v", got)
	}
}

func TestParseSkillDelimiters(t *testing.T) {
	for content, ok := range map[string]bool{
		"---\ndescription: x\n---\nbody":     true,
		"---\r\ndescription: x\r\n---  \r\n": true,
		"---\ndescription: x\n---oops\nbody": false,
		"---\ndescription: x\n":              false,
		"description: x\n---\n":              false,
	} {
		if _, _, _, err := parseSkill(content); (err == nil) != ok {
			t.Errorf("%q: err %v", content, err)
		}
	}
	if _, _, body, _ := parseSkill("---\r\ndescription: x\r\n---\r\n\r\n# Deploy\r\nsteps\r\n"); body != "# Deploy\nsteps" {
		t.Errorf("body %q", body)
	}
}

// Sessions that use a pinned source say when it wasn't checked for new
// commits for a while, and when a newer commit wasn't accepted.
func TestNotices(t *testing.T) {
	h := testenv.Home(t)
	url, commit := "https://h/team", strings.Repeat("a", 40)
	testenv.Write(t, filepath.Join(source.GenDir(url, commit), "a.md"), "A")
	testenv.Write(t, filepath.Join(h, "n", "me.md"), "me")
	testenv.Write(t, filepath.Join(h, ".config", "ctxtra", "b.yaml"),
		head+"  - {name: team, match: {dirs: [~/work]}, instructions: [ctxtra://team/a.md]}\n  - {name: me, match: {global: true}, instructions: [~/n/me.md]}\n")
	cfgPath := testenv.Config(t, h, "schema: ctxtra/v1\nsources:\n  team: {git: '"+url+"', branch: main, commit: '"+commit+"'}\ninclude: [b.yaml]\n")
	notices := func(dir string) []string {
		t.Helper()
		res, _ := Session(dir, cfgPath)
		if len(res.Problems) > 0 {
			t.Fatalf("problems %v", res.Problems)
		}
		return res.Notices
	}
	work := filepath.Join(h, "work")
	os.MkdirAll(work, 0o755)

	if got := notices(work); !slices.Equal(got, []string{"team not checked for new commits on this machine yet; run ctxtra source update"}) {
		t.Errorf("never checked: %v", got)
	}
	if got := notices(h); len(got) != 0 {
		t.Errorf("a session that doesn't use the source: %v", got)
	}
	id := source.ID(url)
	state.Update(id, url, "main", func(e *state.Source) { e.LastCheck = time.Now().Add(-3 * 24 * time.Hour) })
	if got := notices(work); len(got) != 0 {
		t.Errorf("checked 3 days ago: %v", got)
	}
	state.Update(id, url, "main", func(e *state.Source) {
		e.LastCheck = time.Now().Add(-12*24*time.Hour - time.Hour)
		e.Dismissed = strings.Repeat("b", 40)
	})
	want := []string{"team last checked 12 days ago; run ctxtra source update to check for changes", "team bbbbbbb not accepted (dismissed); you have aaaaaaa"}
	if got := notices(work); !slices.Equal(got, want) {
		t.Errorf("old check and dismissed: %v", got)
	}
	// State about another branch of the repository is kept apart.
	state.Update(id, url, "other", func(e *state.Source) { e.LastCheck = time.Now(); e.Dismissed = "" })
	if got := notices(work); !slices.Equal(got, want) {
		t.Errorf("state of another branch: %v", got)
	}
}
