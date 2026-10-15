package config

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/atelpis/ctxtra/internal/fsutil"
	"github.com/atelpis/ctxtra/internal/testenv"
)

func load(path string) *Config {
	return Load(path, fsutil.NewReader(MaxFileBytes, MaxSessionBytes))
}

const head = "apiVersion: ctxtra/v1alpha1\nkind: Binding\n"

// setup writes a config that includes bindings.yaml and returns the loaded
// config.
func setup(t *testing.T, h, bindings string) *Config {
	t.Helper()
	testenv.Write(t, filepath.Join(h, ".config", "ctxtra", "bindings.yaml"), bindings)
	return load(testenv.Config(t, h, "apiVersion: ctxtra/v1alpha1\nkind: Config\ninclude: [bindings.yaml]\n"))
}

func TestLoad(t *testing.T) {
	h := testenv.Home(t)
	testenv.Write(t, filepath.Join(h, "team", "bindings", "go.yaml"), head+`metadata: {name: go}
spec:
  selector:
    gitRepos:
      - remote: github.com/Acme/**
  files: [../go.md]
  skills: [../skills/release]
`)
	testenv.Write(t, filepath.Join(h, ".config", "ctxtra", "personal.yaml"), head+`metadata: {name: soul}
spec:
  selector: {global: true}
  files: [~/notes/me.md]
---
`+head+`metadata:
  name: work
spec:
  selector:
    dirs: [~/dev/work/**]
    gitRepos:
      - remote: github.com/acme/mono
        subpath: ./services/billing/
  files: [notes/work.md, /abs/x.md]
`)
	cfg := load(testenv.Config(t, h, `apiVersion: ctxtra/v1alpha1
kind: Config
include:
  - personal.yaml
  - ~/team/bindings/go.yaml
budget:
  maxChars: 40000
`))
	if len(cfg.Problems) > 0 {
		t.Fatalf("problems: %v", cfg.Problems)
	}
	if cfg.MaxChars != 40000 || len(cfg.Includes) != 2 || len(cfg.Bindings) != 3 {
		t.Fatalf("got %+v", cfg)
	}
	soul, work, goB := cfg.Bindings[0], cfg.Bindings[1], cfg.Bindings[2]
	if soul.Name != "soul" || !soul.Selector.Global || soul.Files[0] != filepath.Join(h, "notes", "me.md") {
		t.Errorf("soul: %+v", soul)
	}
	conf := filepath.Join(h, ".config", "ctxtra")
	if work.Files[0] != filepath.Join(conf, "notes", "work.md") || work.Files[1] != "/abs/x.md" {
		t.Errorf("work files relative to the binding file: %v", work.Files)
	}
	if work.Selector.Dirs[0] != h+"/dev/work/**" || work.Selector.GitRepos[0].Subpath != "services/billing" {
		t.Errorf("work selector: %+v", work.Selector)
	}
	if goB.Selector.GitRepos[0].Remote != "github.com/acme/**" {
		t.Errorf("remote patterns are lower case: %+v", goB.Selector)
	}
	if goB.Files[0] != filepath.Join(h, "team", "go.md") || goB.Skills[0] != filepath.Join(h, "team", "skills", "release") {
		t.Errorf("go paths: %v %v", goB.Files, goB.Skills)
	}
	if id := goB.ID(); id != "go (~/team/bindings/go.yaml)" {
		t.Errorf("ID = %q", id)
	}
}

// Every broken binding is skipped and reported; it never applies.
func TestStrictBindings(t *testing.T) {
	h := testenv.Home(t)
	ok := "metadata: {name: a}\nspec:\n  selector: {global: true}\n  files: [x.md]\n"
	cases := map[string]string{
		"no apiVersion":       "kind: Binding\n" + ok,
		"old apiVersion":      "apiVersion: ctxtra/v1\nkind: Binding\n" + ok,
		"wrong kind":          "apiVersion: ctxtra/v1alpha1\nkind: Config\n" + ok,
		"no name":             head + "metadata: {}\nspec:\n  selector: {global: true}\n  files: [x.md]\n",
		"bad name":            head + "metadata: {name: 'a b'}\nspec:\n  selector: {global: true}\n  files: [x.md]\n",
		"unknown key":         head + ok + "  file: [y.md]\n",
		"no selector":         head + "metadata: {name: a}\nspec:\n  files: [x.md]\n",
		"empty selector":      head + "metadata: {name: a}\nspec:\n  selector: {}\n  files: [x.md]\n",
		"global false":        head + "metadata: {name: a}\nspec:\n  selector: {global: false}\n  files: [x.md]\n",
		"global string":       head + "metadata: {name: a}\nspec:\n  selector: {global: 'true'}\n  files: [x.md]\n",
		"typo in selector":    head + "metadata: {name: a}\nspec:\n  selector: {gitRepo: [{remote: x}]}\n  files: [x.md]\n",
		"null remote":         head + "metadata: {name: a}\nspec:\n  selector: {gitRepos: [{remote: null}]}\n  files: [x.md]\n",
		"subpath only":        head + "metadata: {name: a}\nspec:\n  selector: {gitRepos: [{subpath: src}]}\n  files: [x.md]\n",
		"subpath escapes":     head + "metadata: {name: a}\nspec:\n  selector: {gitRepos: [{remote: x, subpath: ../y}]}\n  files: [x.md]\n",
		"empty dirs":          head + "metadata: {name: a}\nspec:\n  selector: {dirs: []}\n  files: [x.md]\n",
		"relative dir":        head + "metadata: {name: a}\nspec:\n  selector: {dirs: [dev/x]}\n  files: [x.md]\n",
		"no files or skills":  head + "metadata: {name: a}\nspec:\n  selector: {global: true}\n",
		"glob in files":       head + "metadata: {name: a}\nspec:\n  selector: {global: true}\n  files: [notes/*.md]\n",
		"null in files":       head + "metadata: {name: a}\nspec:\n  selector: {global: true}\n  files: [null]\n",
		"files not a list":    head + "metadata: {name: a}\nspec:\n  selector: {global: true}\n  files: x.md\n",
		"duplicate key":       head + "metadata: {name: a}\nspec:\n  selector: {global: true}\n  selector: {dirs: [/x]}\n  files: [x.md]\n",
		"alias":               head + "metadata: {name: &n a}\nspec:\n  selector: {global: true}\n  files: [*n]\n",
		"top level is a list": "- a\n",
	}
	for name, y := range cases {
		cfg := setup(t, h, y)
		if len(cfg.Bindings) != 0 || len(cfg.Problems) == 0 {
			t.Errorf("%s: bindings=%d problems=%v; want none and a problem", name, len(cfg.Bindings), cfg.Problems)
		}
	}
	// A broken binding doesn't disable the good ones in the same file, and
	// names are unique per file.
	cfg := setup(t, h, head+"metadata: {name: a}\nspec: {selector: {dirz: [/x]}, files: [x.md]}\n---\n"+head+ok+"---\n"+head+ok)
	if len(cfg.Bindings) != 1 || len(cfg.Problems) != 2 || !strings.Contains(cfg.Problems[1], "duplicate name") {
		t.Errorf("bindings=%d problems=%v", len(cfg.Bindings), cfg.Problems)
	}
}

func TestStrictConfig(t *testing.T) {
	h := testenv.Home(t)
	testenv.Write(t, filepath.Join(h, ".config", "ctxtra", "b.yaml"), head+"metadata: {name: a}\nspec: {selector: {global: true}, files: [x.md]}\n")
	cases := map[string]string{
		"no apiVersion":      "kind: Config\ninclude: [b.yaml]\n",
		"wrong kind":         "apiVersion: ctxtra/v1alpha1\nkind: Binding\ninclude: [b.yaml]\n",
		"unknown key":        "apiVersion: ctxtra/v1alpha1\nkind: Config\ninclude: [b.yaml]\nrules: []\n",
		"two documents":      "apiVersion: ctxtra/v1alpha1\nkind: Config\ninclude: [b.yaml]\n---\nkind: Config\n",
		"include not a list": "apiVersion: ctxtra/v1alpha1\nkind: Config\ninclude: b.yaml\n",
		"broken YAML":        "apiVersion: [",
	}
	for name, y := range cases {
		cfg := load(testenv.Config(t, h, y))
		if len(cfg.Bindings) != 0 || len(cfg.Problems) == 0 {
			t.Errorf("%s: bindings=%d problems=%v", name, len(cfg.Bindings), cfg.Problems)
		}
	}
	// A broken budget rejects the whole config: the bindings without the
	// budget would add more than intended.
	for _, b := range []string{"{maxChars: -1}", "{maxChars: '5'}", "{maxchars: 5}", "5"} {
		cfg := load(testenv.Config(t, h, "apiVersion: ctxtra/v1alpha1\nkind: Config\ninclude: [b.yaml]\nbudget: "+b+"\n"))
		if len(cfg.Problems) == 0 || len(cfg.Bindings) != 0 {
			t.Errorf("budget %s: bindings=%d problems=%v", b, len(cfg.Bindings), cfg.Problems)
		}
	}
	cfg := load(testenv.Config(t, h, "apiVersion: ctxtra/v1alpha1\nkind: Config\ninclude: [b.yaml, ./b.yaml, missing.yaml]\n"))
	if len(cfg.Bindings) != 1 || len(cfg.Problems) != 2 {
		t.Errorf("duplicate and missing includes: bindings=%d problems=%v", len(cfg.Bindings), cfg.Problems)
	}
	if cfg := load(filepath.Join(h, "nope.yaml")); len(cfg.Problems) != 0 || len(cfg.Bindings) != 0 {
		t.Errorf("a missing config is not a problem: %v", cfg.Problems)
	}
}

// A document that parses but can't be used is skipped alone; a YAML syntax
// error still fails the whole file.
func TestBrokenDocumentIsSkippedAlone(t *testing.T) {
	h := testenv.Home(t)
	good := head + "metadata: {name: good}\nspec: {selector: {global: true}, files: [a.md]}\n"
	cfg := setup(t, h, good+"---\n- a list\n---\n"+head+"metadata: &m {name: anchored}\nspec: {selector: {global: true}, files: [b.md]}\n")
	if len(cfg.Bindings) != 1 || cfg.Bindings[0].Name != "good" || len(cfg.Problems) != 2 {
		t.Errorf("bindings %v, problems %v", cfg.Bindings, cfg.Problems)
	}
	cfg = setup(t, h, good+"---\nspec: [\n")
	if len(cfg.Bindings) != 0 || len(cfg.Problems) != 1 {
		t.Errorf("syntax error: bindings %v, problems %v", cfg.Bindings, cfg.Problems)
	}
}
