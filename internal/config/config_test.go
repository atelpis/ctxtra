package config

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/atelpis/ctxtra/internal/fsutil"
	"github.com/atelpis/ctxtra/internal/source"
	"github.com/atelpis/ctxtra/internal/state"
	"github.com/atelpis/ctxtra/internal/testenv"
)

func load(path string) *Config {
	return Load(path, fsutil.NewReader(MaxFileBytes, MaxSessionBytes))
}

const head = "schema: ctxtra/v1\nbindings:\n"

// setup writes a config that includes bindings.yaml and returns the loaded
// config.
func setup(t *testing.T, h, bindings string) *Config {
	t.Helper()
	testenv.Write(t, filepath.Join(h, ".config", "ctxtra", "bindings.yaml"), bindings)
	return load(testenv.Config(t, h, "schema: ctxtra/v1\ninclude: [bindings.yaml]\n"))
}

func TestLoad(t *testing.T) {
	h := testenv.Home(t)
	testenv.Write(t, filepath.Join(h, "team", "bindings", "go.yaml"), head+`  - name: go
    match:
      repos:
        - remote: github.com/Acme/**
    instructions: [../go.md]
    skills: [../skills/release]
`)
	testenv.Write(t, filepath.Join(h, ".config", "ctxtra", "personal.yaml"), head+`  - name: soul
    match: {global: true}
    instructions: [~/notes/me.md]
  - name: work
    match:
      dirs: [~/dev/work/**]
      repos:
        - remote: github.com/acme/mono
          subpath: ./services/billing/
        - github.com/acme/OPS
    instructions: [notes/work.md, /abs/x.md]
`)
	cfg := load(testenv.Config(t, h, `schema: ctxtra/v1
bindings:
  - name: own
    match: {dirs: [~/own]}
    instructions: [own.md]
include:
  - personal.yaml
  - ~/team/bindings/go.yaml
budget:
  max-chars: 40000
`))
	if len(cfg.Problems) > 0 {
		t.Fatalf("problems: %v", cfg.Problems)
	}
	if cfg.MaxChars != 40000 || len(cfg.BindingFiles) != 2 || len(cfg.Bindings) != 4 {
		t.Fatalf("got %+v", cfg)
	}
	own, soul, work, goB := cfg.Bindings[0], cfg.Bindings[1], cfg.Bindings[2], cfg.Bindings[3]
	conf := filepath.Join(h, ".config", "ctxtra")
	if own.Name != "own" || own.Instructions[0] != filepath.Join(conf, "own.md") || own.ID() != "own (~/.config/ctxtra/config.yaml)" {
		t.Errorf("the config's own binding comes first, relative to the config: %+v", own)
	}
	if soul.Name != "soul" || !soul.Selector.Global || soul.Instructions[0] != filepath.Join(h, "notes", "me.md") {
		t.Errorf("soul: %+v", soul)
	}
	if work.Instructions[0] != filepath.Join(conf, "notes", "work.md") || work.Instructions[1] != "/abs/x.md" {
		t.Errorf("work instructions relative to the binding file: %v", work.Instructions)
	}
	if work.Selector.Dirs[0] != h+"/dev/work/**" || work.Selector.GitRepos[0].Subpath != "services/billing" ||
		work.Selector.GitRepos[1] != (GitRepo{Remote: "github.com/acme/ops"}) {
		t.Errorf("work match: %+v", work.Selector)
	}
	if goB.Selector.GitRepos[0].Remote != "github.com/acme/**" {
		t.Errorf("remote patterns are lower case: %+v", goB.Selector)
	}
	if goB.Instructions[0] != filepath.Join(h, "team", "go.md") || goB.Skills[0] != filepath.Join(h, "team", "skills", "release") {
		t.Errorf("go paths: %v %v", goB.Instructions, goB.Skills)
	}
	if id := goB.ID(); id != "go (~/team/bindings/go.yaml)" {
		t.Errorf("ID = %q", id)
	}
}

// Every broken binding is skipped and reported; it never applies.
func TestStrictBindings(t *testing.T) {
	h := testenv.Home(t)
	item := func(s string) string { return head + "  - " + s + "\n" }
	cases := map[string]string{
		"no name":          item("{match: {global: true}, instructions: [x.md]}"),
		"bad name":         item("{name: 'a b', match: {global: true}, instructions: [x.md]}"),
		"unknown key":      item("{name: a, match: {global: true}, instructions: [x.md], files: [y.md]}"),
		"no match":         item("{name: a, instructions: [x.md]}"),
		"empty match":      item("{name: a, match: {}, instructions: [x.md]}"),
		"global false":     item("{name: a, match: {global: false}, instructions: [x.md]}"),
		"global string":    item("{name: a, match: {global: 'true'}, instructions: [x.md]}"),
		"typo in match":    item("{name: a, match: {repo: [x]}, instructions: [x.md]}"),
		"null remote":      item("{name: a, match: {repos: [{remote: null}]}, instructions: [x.md]}"),
		"null repo":        item("{name: a, match: {repos: [null]}, instructions: [x.md]}"),
		"empty repo":       item("{name: a, match: {repos: ['']}, instructions: [x.md]}"),
		"subpath only":     item("{name: a, match: {repos: [{subpath: src}]}, instructions: [x.md]}"),
		"subpath escapes":  item("{name: a, match: {repos: [{remote: x, subpath: ../y}]}, instructions: [x.md]}"),
		"empty dirs":       item("{name: a, match: {dirs: []}, instructions: [x.md]}"),
		"relative dir":     item("{name: a, match: {dirs: [dev/x]}, instructions: [x.md]}"),
		"no instructions":  item("{name: a, match: {global: true}}"),
		"glob":             item("{name: a, match: {global: true}, instructions: [notes/*.md]}"),
		"null instruction": item("{name: a, match: {global: true}, instructions: [null]}"),
		"not a list":       item("{name: a, match: {global: true}, instructions: x.md}"),
		"duplicate key":    item("{name: a, match: {global: true}, match: {dirs: [/x]}, instructions: [x.md]}"),
		"a string":         item("x.yaml"),
		"a list":           item("[a]"),
	}
	for name, y := range cases {
		cfg := setup(t, h, y)
		if len(cfg.Bindings) != 0 || len(cfg.Problems) != 1 || !strings.Contains(cfg.Problems[0], "binding skipped") {
			t.Errorf("%s: bindings=%d problems=%v; want none and a problem", name, len(cfg.Bindings), cfg.Problems)
		}
	}
	// A broken binding doesn't disable the good ones in the same file, and
	// names are unique per file.
	ok := "  - {name: a, match: {global: true}, instructions: [x.md]}\n"
	cfg := setup(t, h, head+"  - {name: a, match: {dirz: [/x]}, instructions: [x.md]}\n"+ok+ok)
	if len(cfg.Bindings) != 1 || len(cfg.Problems) != 2 || !strings.Contains(cfg.Problems[1], "duplicate name") {
		t.Errorf("bindings=%d problems=%v", len(cfg.Bindings), cfg.Problems)
	}
}

// A binding file that can't be read as a whole is skipped as a whole.
func TestStrictBindingFile(t *testing.T) {
	h := testenv.Home(t)
	ok := "  - {name: a, match: {global: true}, instructions: [x.md]}\n"
	cases := map[string]string{
		"no schema":           "bindings:\n" + ok,
		"old schema":          "schema: ctxtra/v1alpha1\nbindings:\n" + ok,
		"schema not a string": "schema: [ctxtra/v1]\nbindings:\n" + ok,
		"unknown key":         head + ok + "rules: []\n",
		"no bindings":         "schema: ctxtra/v1\n",
		"empty bindings":      "schema: ctxtra/v1\nbindings: []\n",
		"bindings not a list": "schema: ctxtra/v1\nbindings: {a: b}\n",
		"two documents":       head + ok + "---\n" + head + ok,
		"alias":               head + "  - {name: &n a, match: {global: true}, instructions: [x.md]}\n  - {name: b, match: {global: true}, instructions: [*n]}\n",
		"top level is a list": "- a\n",
		"syntax error":        head + ok + "  - [\n",
		"empty":               "",
	}
	for name, y := range cases {
		cfg := setup(t, h, y)
		if len(cfg.Bindings) != 0 || len(cfg.Problems) != 1 {
			t.Errorf("%s: bindings=%d problems=%v; want none and a problem", name, len(cfg.Bindings), cfg.Problems)
		}
	}
	// The keys of the config are refused by name, also in front of good
	// bindings: a binding file can't choose sources, other files or the
	// budget.
	for _, k := range []string{"sources: {a: {trusted-path: /a}}", "include: [x.yaml]", "budget: {max-chars: 5}"} {
		cfg := setup(t, h, head+ok+k+"\n")
		if len(cfg.Bindings) != 0 || len(cfg.Problems) != 1 ||
			!strings.Contains(cfg.Problems[0], " works only in the config (~/.config/ctxtra/config.yaml); nothing from this file is used") {
			t.Errorf("%s: bindings=%d problems=%v", k, len(cfg.Bindings), cfg.Problems)
		}
	}
}

func TestStrictConfig(t *testing.T) {
	h := testenv.Home(t)
	testenv.Write(t, filepath.Join(h, ".config", "ctxtra", "b.yaml"), head+"  - {name: a, match: {global: true}, instructions: [x.md]}\n")
	cases := map[string]string{
		"no schema":           "include: [b.yaml]\n",
		"wrong schema":        "schema: ctxtra/v2\ninclude: [b.yaml]\n",
		"unknown key":         "schema: ctxtra/v1\ninclude: [b.yaml]\nrules: []\n",
		"two documents":       "schema: ctxtra/v1\ninclude: [b.yaml]\n---\nschema: ctxtra/v1\n",
		"include not a list":  "schema: ctxtra/v1\ninclude: b.yaml\n",
		"bindings not a list": "schema: ctxtra/v1\ninclude: [b.yaml]\nbindings: {a: b}\n",
		"empty bindings":      "schema: ctxtra/v1\ninclude: [b.yaml]\nbindings: []\n",
		"broken YAML":         "schema: [",
	}
	for name, y := range cases {
		cfg := load(testenv.Config(t, h, y))
		if len(cfg.Bindings) != 0 || len(cfg.Problems) == 0 {
			t.Errorf("%s: bindings=%d problems=%v", name, len(cfg.Bindings), cfg.Problems)
		}
	}
	if cfg := load(testenv.Config(t, h, "schema: ctxtra/v9\n")); len(cfg.Problems) != 1 ||
		!strings.Contains(cfg.Problems[0], `schema must be ctxtra/v1 (found "ctxtra/v9")`) {
		t.Errorf("wrong schema: %v", cfg.Problems)
	}
	// A broken budget rejects the whole config: the bindings without the
	// budget would add more than intended.
	for _, b := range []string{"{max-chars: -1}", "{max-chars: '5'}", "{maxChars: 5}", "5"} {
		cfg := load(testenv.Config(t, h, "schema: ctxtra/v1\nbindings: [{name: own, match: {global: true}, instructions: [x.md]}]\ninclude: [b.yaml]\nbudget: "+b+"\n"))
		if len(cfg.Problems) == 0 || len(cfg.Bindings) != 0 {
			t.Errorf("budget %s: bindings=%d problems=%v", b, len(cfg.Bindings), cfg.Problems)
		}
	}
	cfg := load(testenv.Config(t, h, "schema: ctxtra/v1\ninclude: [b.yaml, ./b.yaml, missing.yaml, config.yaml]\n"))
	if len(cfg.Bindings) != 1 || len(cfg.Problems) != 3 || !strings.Contains(cfg.Problems[2], "config.yaml is the config itself") {
		t.Errorf("duplicate, missing and self include: bindings=%d problems=%v", len(cfg.Bindings), cfg.Problems)
	}
	if cfg := load(filepath.Join(h, "nope.yaml")); len(cfg.Problems) != 0 || len(cfg.Bindings) != 0 {
		t.Errorf("a missing config is not a problem: %v", cfg.Problems)
	}
}

// A broken binding in the config is skipped alone, like one in a binding
// file: the config's sources, budget and other bindings still apply. A
// binding file listed under bindings gets a hint.
func TestConfigOwnBindings(t *testing.T) {
	h := testenv.Home(t)
	cfg := load(testenv.Config(t, h, `schema: ctxtra/v1
sources:
  dot: {trusted-path: ~/dot}
bindings:
  - {name: a, match: {global: true}, instructions: [a.md, ctxtra://dot/x.md]}
  - {name: a, match: {global: true}, instructions: [b.md]}
  - {name: c, match: {gitRepo: [x]}, instructions: [c.md]}
  - bindings.yaml
budget: {max-chars: 10}
`))
	if len(cfg.Bindings) != 1 || cfg.Bindings[0].Name != "a" || cfg.MaxChars != 10 ||
		!slices.Equal(cfg.Bindings[0].Instructions, []string{filepath.Join(h, ".config", "ctxtra", "a.md"), filepath.Join(h, "dot", "x.md")}) {
		t.Errorf("bindings %+v", cfg.Bindings)
	}
	if len(cfg.Problems) != 3 || !strings.Contains(cfg.Problems[0], "duplicate name") ||
		!strings.Contains(cfg.Problems[2], "a binding file goes under include, in the config") {
		t.Errorf("problems %v", cfg.Problems)
	}
	// Check, which add and sync run before they change the config, doesn't
	// check the bindings one by one.
	if err := Check([]byte("schema: ctxtra/v1\nbindings: [x.yaml]\n")); err != nil {
		t.Errorf("Check: %v", err)
	}
}

func TestSources(t *testing.T) {
	h := testenv.Home(t)
	dot := testenv.Repo(t, filepath.Join(h, "dev", "dotfiles"), "git@github.com:me/dotfiles.git")
	testenv.Write(t, filepath.Join(dot, "ctxtra", "work.yaml"), head+`  - name: work
    match: {global: true}
    instructions: [work.md, ctxtra://dotfiles/notes/me.md, ~/notes/x.md]
    skills: [ctxtra://dotfiles/skills/deploy]
`)
	cfg := load(testenv.Config(t, h, `schema: ctxtra/v1
sources:
  dotfiles:
    trusted-path: ~/dev/dotfiles
include:
  - ctxtra://dotfiles/ctxtra/work.yaml
`))
	if len(cfg.Problems) > 0 || len(cfg.Bindings) != 1 {
		t.Fatalf("bindings=%d problems=%v", len(cfg.Bindings), cfg.Problems)
	}
	b := cfg.Bindings[0]
	want := []string{filepath.Join(dot, "ctxtra", "work.md"), filepath.Join(dot, "notes", "me.md"), filepath.Join(h, "notes", "x.md")}
	if !slices.Equal(b.Instructions, want) || b.Skills[0] != filepath.Join(dot, "skills", "deploy") {
		t.Errorf("instructions %v, skills %v", b.Instructions, b.Skills)
	}
	if len(cfg.Sources) != 1 || cfg.Sources[0] != (Source{Name: "dotfiles", Line: 3, TrustedPath: dot}) {
		t.Errorf("sources %v", cfg.Sources)
	}
}

// A binding file in a repository with a remote is read only from a trusted
// source.
func TestUntrustedBindingFile(t *testing.T) {
	h := testenv.Home(t)
	team := testenv.Repo(t, filepath.Join(h, "team"), "git@github.com:acme/team.git")
	testenv.Write(t, filepath.Join(team, "b.yaml"), head+"  - {name: a, match: {global: true}, instructions: [~/.ssh/id]}\n")
	cfg := load(testenv.Config(t, h, "schema: ctxtra/v1\ninclude: [~/team/b.yaml]\n"))
	if len(cfg.Bindings) != 0 || len(cfg.Problems) != 1 || !strings.Contains(cfg.Problems[0], "is not a trusted source") {
		t.Errorf("bindings=%d problems=%v", len(cfg.Bindings), cfg.Problems)
	}
}

// A relative config path is made absolute, so that the trust check sees the
// file that is read.
func TestRelativeConfigPath(t *testing.T) {
	h := testenv.Home(t)
	team := testenv.Repo(t, filepath.Join(h, "team"), "git@github.com:acme/team.git")
	testenv.Write(t, filepath.Join(team, "b.yaml"), head+"  - {name: a, match: {global: true}, instructions: [~/.ssh/id]}\n")
	testenv.Write(t, filepath.Join(team, "config.yaml"), "schema: ctxtra/v1\ninclude: [b.yaml]\n")
	t.Chdir(team)
	cfg := load("config.yaml")
	if len(cfg.Bindings) != 0 || len(cfg.Problems) != 1 || !strings.Contains(cfg.Problems[0], "is not a trusted source") {
		t.Errorf("bindings=%d problems=%v", len(cfg.Bindings), cfg.Problems)
	}
}

func TestBadSources(t *testing.T) {
	h := testenv.Home(t)
	testenv.Write(t, filepath.Join(h, ".config", "ctxtra", "b.yaml"), head+"  - {name: a, match: {global: true}, instructions: [x.md]}\n")
	const cfgHead = "schema: ctxtra/v1\ninclude: [b.yaml]\n"
	cases := map[string]string{
		"empty":            cfgHead + "sources: {}\n",
		"list":             cfgHead + "sources: [a]\n",
		"no kind":          cfgHead + "sources: {a: {}}\n",
		"unknown kind":     cfgHead + "sources: {a: {git: x}}\n",
		"two kinds":        cfgHead + "sources: {a: {trusted-path: /x, git: y}}\n",
		"relative path":    cfgHead + "sources: {a: {trusted-path: x}}\n",
		"bad name":         cfgHead + "sources: {a/b: {trusted-path: /x}}\n",
		"null path":        cfgHead + "sources: {a: {trusted-path: ~}}\n",
		"duplicate source": cfgHead + "sources:\n  a: {trusted-path: /x}\n  a: {trusted-path: /y}\n",
	}
	for name, y := range cases {
		cfg := load(testenv.Config(t, h, y))
		if len(cfg.Bindings) != 0 || len(cfg.Problems) != 1 {
			t.Errorf("%s: bindings=%d problems=%v", name, len(cfg.Bindings), cfg.Problems)
		}
	}
}

func TestBadReferences(t *testing.T) {
	h := testenv.Home(t)
	for _, ref := range []string{"ctxtra://nope/a.md", "ctxtra://dot", "ctxtra://dot/", "ctxtra://dot/../x.md", "ctxtra://dot/a/../../x.md", "ctxtra://dot//etc/passwd"} {
		testenv.Write(t, filepath.Join(h, ".config", "ctxtra", "b.yaml"), head+"  - {name: a, match: {global: true}, instructions: ['"+ref+"']}\n")
		cfg := load(testenv.Config(t, h, "schema: ctxtra/v1\nsources: {dot: {trusted-path: ~/dot}}\ninclude: [b.yaml, '"+ref+"']\n"))
		if len(cfg.Bindings) != 0 || len(cfg.Problems) != 2 {
			t.Errorf("%s: bindings=%d problems=%v", ref, len(cfg.Bindings), cfg.Problems)
		}
	}
}

// The store is read only through ctxtra:// references: by a plain path or a
// symlink, a pinned source's binding file would escape its limits.
func TestStoreByPlainPath(t *testing.T) {
	h := testenv.Home(t)
	gen := filepath.Join(h, ".ctxtra", "store", "0123456789abcdef", strings.Repeat("a", 40))
	testenv.Write(t, filepath.Join(gen, "b.yaml"), head+"  - {name: a, match: {global: true}, instructions: [~/.ssh/id]}\n")
	symlink := filepath.Join(h, "link.yaml")
	if err := os.Symlink(filepath.Join(gen, "b.yaml"), symlink); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{filepath.Join(gen, "b.yaml"), symlink} {
		cfg := load(testenv.Config(t, h, "schema: ctxtra/v1\ninclude: ['"+p+"']\n"))
		if len(cfg.Bindings) != 0 || len(cfg.Problems) != 1 || !strings.Contains(cfg.Problems[0], "can't be referenced by its path") {
			t.Errorf("%s: bindings=%d problems=%v", p, len(cfg.Bindings), cfg.Problems)
		}
	}
}

// A pinned source that isn't in the store skips only its references: the
// user's own files in the same binding still apply.
func TestUnavailablePinnedSource(t *testing.T) {
	h := testenv.Home(t)
	testenv.Write(t, filepath.Join(h, ".config", "ctxtra", "b.yaml"), head+"  - {name: a, match: {global: true}, instructions: [~/me.md, ctxtra://team/x.md, ctxtra://new/y.md]}\n")
	cfg := load(testenv.Config(t, h, `schema: ctxtra/v1
sources:
  team: {git: https://h/team, branch: main, commit: '`+strings.Repeat("a", 40)+`'}
  new: {git: https://h/new, branch: main}
include: [b.yaml, ctxtra://team/b.yaml]
`))
	if len(cfg.Bindings) != 1 || !slices.Equal(cfg.Bindings[0].Instructions, []string{filepath.Join(h, "me.md")}) {
		t.Errorf("bindings %v", cfg.Bindings)
	}
	want := []string{"ctxtra://team/x.md: team aaaaaaa is not in ctxtra's store; run: ctxtra sync --restore",
		"ctxtra://new/y.md: the source new has no accepted commit yet; run: ctxtra sync new", "ctxtra://team/b.yaml: team aaaaaaa"}
	if len(cfg.Problems) != 3 {
		t.Fatalf("problems %v", cfg.Problems)
	}
	for i, w := range want {
		if !strings.Contains(cfg.Problems[i], w) {
			t.Errorf("problem %d: %s, want %q", i, cfg.Problems[i], w)
		}
	}
}

func TestLocalGitSource(t *testing.T) {
	h := testenv.Home(t)
	c := strings.Repeat("a", 40)
	cfg := load(testenv.Config(t, h, "schema: ctxtra/v1\nsources:\n  dev: {local-git: ~/dev/team, commit: '"+c+"'}\n"))
	if len(cfg.Problems) > 0 || cfg.Sources[0].LocalGit != filepath.Join(h, "dev", "team") || !cfg.Sources[0].Pinned() ||
		cfg.Sources[0].Gen() != source.GenDir(filepath.Join(h, "dev", "team"), c) {
		t.Errorf("%+v %v", cfg.Sources, cfg.Problems)
	}
	for _, y := range []string{"{local-git: x}", "{local-git: /x, branch: main}", "{local-git: /x, git: https://h/x, branch: main}", "{trusted-path: /x, commit: '" + c + "'}"} {
		if cfg := load(testenv.Config(t, h, "schema: ctxtra/v1\nsources:\n  dev: "+y+"\n")); len(cfg.Problems) != 1 {
			t.Errorf("%s: %v", y, cfg.Problems)
		}
	}
}

// Plain paths under an imported clone are served from its source; a
// trusted-path source that covers them comes first; when the source is
// gone, they are skipped.
func TestCloneMapping(t *testing.T) {
	h := testenv.Home(t)
	url, c := "https://h/team", strings.Repeat("a", 40)
	clone := filepath.Join(h, "dev", "team")
	testenv.Write(t, filepath.Join(clone, "a.md"), "on disk")
	testenv.Write(t, filepath.Join(source.GenDir(url, c), "a.md"), "in the store")
	if _, err := state.Map(clone, "team", url); err != nil {
		t.Fatal(err)
	}
	testenv.Write(t, filepath.Join(h, ".config", "ctxtra", "b.yaml"), head+"  - {name: a, match: {global: true}, instructions: [~/dev/team/a.md]}\n")
	files := func(sources string) ([]string, []string) {
		cfg := load(testenv.Config(t, h, "schema: ctxtra/v1\n"+sources+"include: [b.yaml]\n"))
		if len(cfg.Bindings) == 0 {
			return nil, cfg.Problems
		}
		return cfg.Bindings[0].Instructions, cfg.Problems
	}
	got, problems := files("sources:\n  team: {git: '" + url + "', branch: main, commit: '" + c + "'}\n")
	if !slices.Equal(got, []string{filepath.Join(source.GenDir(url, c), "a.md")}) || len(problems) != 0 {
		t.Errorf("mapped: %v %v", got, problems)
	}
	got, _ = files("sources:\n  team: {git: '" + url + "', branch: main, commit: '" + c + "'}\n  live: {trusted-path: ~/dev/team}\n")
	if !slices.Equal(got, []string{filepath.Join(clone, "a.md")}) {
		t.Errorf("trusted first: %v", got)
	}
	got, problems = files("")
	if len(got) != 0 || len(problems) != 1 || !strings.Contains(problems[0], "not in the config any more; skipped, not read from disk") {
		t.Errorf("source removed: %v %v", got, problems)
	}
	// Another source with the same URL is not the imported one: the mapping
	// is by name and URL, whatever the order.
	other := "  dev: {git: '" + url + "', branch: dev, commit: '" + strings.Repeat("b", 40) + "'}\n"
	got, problems = files("sources:\n" + other)
	if len(got) != 0 || len(problems) != 1 {
		t.Errorf("only another alias: %v %v", got, problems)
	}
	got, _ = files("sources:\n" + other + "  team: {git: '" + url + "', branch: main, commit: '" + c + "'}\n")
	if !slices.Equal(got, []string{filepath.Join(source.GenDir(url, c), "a.md")}) {
		t.Errorf("two aliases: %v", got)
	}
	if _, err := state.Map(filepath.Join(clone, "sub"), "other", "https://h/other"); err == nil {
		t.Error("a mapping inside a mapped folder")
	}
	// With a broken mappings file, ctxtra can't tell whether a path was
	// mapped: plain paths are skipped, also where the clone was replaced by a
	// folder outside git, except under a trusted-path source.
	os.WriteFile(state.MappingsPath(), []byte("{"), 0o600)
	os.RemoveAll(filepath.Join(clone, ".git"))
	got, problems = files("")
	if len(got) != 0 || len(problems) != 1 || !strings.Contains(problems[0], "mappings.json can't be read") {
		t.Errorf("broken mappings: %v %v", got, problems)
	}
	testenv.Write(t, filepath.Join(clone, "b.yaml"), head+"  - {name: a, match: {global: true}, instructions: [a.md]}\n")
	cfg := load(testenv.Config(t, h, "schema: ctxtra/v1\nsources:\n  live: {trusted-path: ~/dev/team}\ninclude: [ctxtra://live/b.yaml]\n"))
	if len(cfg.Bindings) != 1 || !slices.Equal(cfg.Bindings[0].Instructions, []string{filepath.Join(clone, "a.md")}) || len(cfg.Problems) != 0 {
		t.Errorf("broken mappings, trusted: %v %v", cfg.Bindings, cfg.Problems)
	}
	if _, err := state.Map(filepath.Join(h, "x"), "x", "https://h/x"); err == nil {
		t.Error("a broken mappings file was written")
	}
}

// While cleaning holds the store, a session waits once, not once per
// reference.
func TestStoreBusyWaitsOnce(t *testing.T) {
	h := testenv.Home(t)
	c1, c2 := strings.Repeat("a", 40), strings.Repeat("b", 40)
	for _, c := range []string{c1, c2} {
		for _, f := range []string{"x.md", "y.md", "z.md"} {
			testenv.Write(t, filepath.Join(source.GenDir("https://h/"+c[:1], c), f), f)
		}
	}
	testenv.Write(t, filepath.Join(h, ".config", "ctxtra", "b.yaml"), head+"  - {name: a, match: {global: true}, instructions: [~/me.md, ctxtra://a/x.md, ctxtra://a/y.md, ctxtra://b/z.md]}\n")
	cfgPath := testenv.Config(t, h, "schema: ctxtra/v1\nsources:\n  a: {git: 'https://h/a', branch: main, commit: '"+c1+"'}\n  b: {git: 'https://h/b', branch: main, commit: '"+c2+"'}\ninclude: [b.yaml]\n")
	unlock, err := fsutil.Lock(filepath.Join(h, ".ctxtra", "store.lock"))
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	start := time.Now()
	cfg := load(cfgPath)
	if d := time.Since(start); d > 1500*time.Millisecond {
		t.Errorf("load took %v", d)
	}
	if len(cfg.Bindings) != 1 || !slices.Equal(cfg.Bindings[0].Instructions, []string{filepath.Join(h, "me.md")}) || len(cfg.Problems) != 3 {
		t.Errorf("bindings %v problems %v", cfg.Bindings, cfg.Problems)
	}
}

// Waits that end well count against the budget too: each touch gets what
// the earlier ones left.
func TestStoreWaitBudget(t *testing.T) {
	h := testenv.Home(t)
	var waits []time.Duration
	old := touch
	t.Cleanup(func() { touch = old })
	touch = func(dir string, wait time.Duration) error {
		waits = append(waits, wait)
		if wait < 450*time.Millisecond {
			time.Sleep(wait)
			return fsutil.ErrLockTimeout
		}
		time.Sleep(450 * time.Millisecond)
		return nil
	}
	var srcs, refs string
	for _, n := range []string{"a", "b", "c", "d"} {
		c := strings.Repeat(n, 40)
		testenv.Write(t, filepath.Join(source.GenDir("https://h/"+n, c), "x.md"), "x")
		srcs += "  " + n + ": {git: 'https://h/" + n + "', branch: main, commit: '" + c + "'}\n"
		refs += ", ctxtra://" + n + "/x.md"
	}
	testenv.Write(t, filepath.Join(h, ".config", "ctxtra", "b.yaml"), head+"  - {name: a, match: {global: true}, instructions: [~/me.md"+refs+"]}\n")
	start := time.Now()
	cfg := load(testenv.Config(t, h, "schema: ctxtra/v1\nsources:\n"+srcs+"include: [b.yaml]\n"))
	if d := time.Since(start); d > 1300*time.Millisecond {
		t.Errorf("load took %v, waits %v", d, waits)
	}
	if len(waits) != 3 || waits[0] != source.TouchWait || waits[1] >= waits[0] || waits[2] >= waits[1] {
		t.Errorf("waits %v", waits)
	}
	if len(cfg.Bindings) != 1 || len(cfg.Bindings[0].Instructions) != 3 || len(cfg.Problems) != 2 {
		t.Errorf("files %v problems %v", cfg.Bindings, cfg.Problems)
	}
}
