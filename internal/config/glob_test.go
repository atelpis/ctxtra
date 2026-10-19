package config

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/atelpis/ctxtra/internal/source"
	"github.com/atelpis/ctxtra/internal/state"
	"github.com/atelpis/ctxtra/internal/testenv"
)

func TestInstructionGlobs(t *testing.T) {
	h := testenv.Home(t)
	for _, p := range []string{"notes/b.md", "notes/a.md", "notes/sub/c.md", "notes/no.txt", "skills/deploy/SKILL.md"} {
		testenv.Write(t, filepath.Join(h, p), "content")
	}
	// Symlinked entries are excluded from wildcard expansion.
	if err := os.Symlink(filepath.Join(h, "notes/a.md"), filepath.Join(h, "notes/link.md")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(h, "notes/sub"), filepath.Join(h, "notes/linkdir")); err != nil {
		t.Fatal(err)
	}
	cfg := setup(t, h, head+`  - name: globs
    match: {global: true}
    instructions: ['~/notes/**/*.md', '~/notes/{b,a,link}.md']
    skills: ['~/skills/*']
`)
	if len(cfg.Problems) > 0 {
		t.Fatal(cfg.Problems)
	}
	want := []string{"notes/a.md", "notes/b.md", "notes/sub/c.md", "notes/a.md", "notes/b.md"}
	for i := range want {
		want[i] = filepath.Join(h, want[i])
	}
	if !slices.Equal(cfg.Bindings[0].Instructions, want) {
		t.Fatalf("instructions: %v", cfg.Bindings[0].Instructions)
	}
	if !slices.Equal(cfg.Bindings[0].Skills, []string{filepath.Join(h, "skills/deploy")}) {
		t.Fatal(cfg.Bindings[0].Skills)
	}
	if err := os.Symlink(filepath.Join(h, "notes"), filepath.Join(h, "alias")); err != nil {
		t.Fatal(err)
	}
	cfg = setup(t, h, head+"  - {name: alias, match: {global: true}, instructions: ['~/alias/**/*.md']}\n")
	if len(cfg.Problems) > 0 || len(cfg.Bindings[0].Instructions) != 3 {
		t.Fatalf("fixed symlink prefix: %+v", cfg)
	}
}

func TestGlobProblems(t *testing.T) {
	h := testenv.Home(t)
	testenv.Repo(t, filepath.Join(h, "untrusted"), "https://github.com/example/team")
	testenv.Write(t, filepath.Join(h, "untrusted/rules.md"), "untrusted")
	for _, pattern := range []string{"~/missing/*.md", "~/untrusted/*.md", "~/notes/{..,sub}/*.md"} {
		cfg := setup(t, h, head+fmt.Sprintf("  - {name: g, match: {global: true}, instructions: ['%s', ~/explicit.md]}\n", pattern))
		if len(cfg.Problems) != 1 || !slices.Equal(cfg.Bindings[0].Instructions, []string{filepath.Join(h, "explicit.md")}) {
			t.Fatalf("%s: %+v", pattern, cfg)
		}
	}
}

func TestGlobTraversalLimit(t *testing.T) {
	h := testenv.Home(t)
	for i := 0; i < maxGlobEntries; i++ {
		testenv.Write(t, filepath.Join(h, "many", fmt.Sprint(i)), "")
	}
	cfg := setup(t, h, head+"  - {name: g, match: {global: true}, instructions: ['~/many/*.md']}\n")
	if len(cfg.Problems) != 1 || !strings.Contains(cfg.Problems[0], "traversal limit") || len(cfg.Bindings[0].Instructions) != 0 {
		t.Fatalf("%+v", cfg)
	}
}

func TestGlobLiteralResolvedPrefix(t *testing.T) {
	h := filepath.Join(testenv.Home(t), "home[old]")
	t.Setenv("HOME", h)
	const remote = "https://example.com/team"
	commit := strings.Repeat("a", 40)
	gen := source.GenDir(remote, commit)
	testenv.Write(t, filepath.Join(gen, "rules.md"), "pinned")
	testenv.Write(t, filepath.Join(strings.Replace(gen, "[old]", "o", 1), "evil.md"), "outside the accepted generation")
	testenv.Write(t, filepath.Join(h, ".config/ctxtra/notes/local.md"), "local")
	cfg := load(testenv.Config(t, h, fmt.Sprintf(`schema: ctxtra/v1
sources:
  team: {git: '%s', branch: main, commit: '%s'}
bindings:
  - name: g
    match: {global: true}
    instructions: ['notes/*.md', 'ctxtra://team/*.md']
`, remote, commit)))
	want := []string{filepath.Join(h, ".config/ctxtra/notes/local.md"), filepath.Join(gen, "rules.md")}
	if len(cfg.Problems) != 0 || len(cfg.Bindings) != 1 || !slices.Equal(cfg.Bindings[0].Instructions, want) {
		t.Fatalf("bindings=%v problems=%v", cfg.Bindings, cfg.Problems)
	}
	if !cfg.Pinned(want[1]) || cfg.Pinned(want[0]) {
		t.Fatal("incorrect pinning")
	}
}

func TestSkillGlobSelectsSkills(t *testing.T) {
	h := testenv.Home(t)
	testenv.Write(t, filepath.Join(h, "skills/.git/config"), "")
	testenv.Skill(t, filepath.Join(h, "skills/deploy"), "description: Deploy")
	cfg := setup(t, h, head+"  - {name: g, match: {global: true}, skills: ['~/skills/**']}\n")
	if len(cfg.Problems) != 0 || !slices.Equal(cfg.Bindings[0].Skills, []string{filepath.Join(h, "skills/deploy")}) {
		t.Fatalf("bindings=%v problems=%v", cfg.Bindings, cfg.Problems)
	}
}

func TestMappedGlobDiagnostics(t *testing.T) {
	h := testenv.Home(t)
	const remote = "https://example.com/team"
	commit := strings.Repeat("a", 40)
	clone := filepath.Join(h, "dev/team")
	testenv.Write(t, filepath.Join(clone, "local.md"), "local")
	gen := source.GenDir(remote, commit)
	testenv.Write(t, filepath.Join(gen, "pinned.md"), "pinned")
	if _, err := state.Map(clone, "team", remote); err != nil {
		t.Fatal(err)
	}
	yaml := fmt.Sprintf(`schema: ctxtra/v1
sources:
  team: {git: '%s', branch: main, commit: '%s'}
bindings:
  - name: g
    match: {global: true}
    instructions: ['~/dev/team/*.md']
`, remote, commit)
	cfg := load(testenv.Config(t, h, yaml))
	if len(cfg.Problems) != 0 || len(cfg.Notes) != 0 || !slices.Equal(cfg.Bindings[0].Instructions, []string{filepath.Join(gen, "pinned.md")}) {
		t.Fatalf("bindings=%v problems=%v notes=%v", cfg.Bindings, cfg.Problems, cfg.Notes)
	}
	cfg = load(testenv.Config(t, h, strings.Replace(yaml, "commit: '"+commit+"'", "commit: '"+strings.Repeat("b", 40)+"'", 1)))
	if len(cfg.Problems) != 1 || strings.Contains(cfg.Problems[0], ".ctxtra-glob-root") || !strings.Contains(cfg.Problems[0], "~/dev/team/*.md") {
		t.Fatal(cfg.Problems)
	}
}
