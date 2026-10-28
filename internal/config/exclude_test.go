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

func TestExclusions(t *testing.T) {
	h := testenv.Home(t)
	for _, p := range []string{"ctx/a.md", "ctx/b.md", "ctx/drafts/c.md", "ctx/drafts/d.md"} {
		testenv.Write(t, filepath.Join(h, p), "content")
	}
	testenv.Skill(t, filepath.Join(h, "skills/deploy"), "description: Deploy")
	testenv.Skill(t, filepath.Join(h, "skills/exp-x"), "description: X")
	testenv.Skill(t, filepath.Join(h, "skills/legacy"), "description: Legacy")
	if err := os.Symlink(filepath.Join(h, "ctx"), filepath.Join(h, "alias")); err != nil {
		t.Fatal(err)
	}
	// An exclusion applies wherever it is in the list.
	cfg := setup(t, h, head+`  - name: x
    match: {global: true}
    instructions: ['!~/ctx/drafts/**', '~/ctx/**/*.md', '!~/ctx/b.md']
    skills: ['~/skills/*', '!../../skills/{exp-*/,legacy/}']
`)
	if len(cfg.Problems) > 0 {
		t.Fatal(cfg.Problems)
	}
	b := cfg.Bindings[0]
	if !slices.Equal(b.Instructions, []string{filepath.Join(h, "ctx/a.md")}) {
		t.Errorf("instructions: %v", b.Instructions)
	}
	if want := []string{filepath.Join(h, "ctx/drafts/c.md"), filepath.Join(h, "ctx/drafts/d.md"), filepath.Join(h, "ctx/b.md")}; !slices.Equal(b.ExcludedInstructions, want) {
		t.Errorf("excluded instructions: %v", b.ExcludedInstructions)
	}
	if !slices.Equal(b.Skills, []string{filepath.Join(h, "skills/deploy")}) || !slices.Equal(b.ExcludedSkills, []string{filepath.Join(h, "skills/exp-x"), filepath.Join(h, "skills/legacy")}) {
		t.Errorf("skills: %v, excluded %v", b.Skills, b.ExcludedSkills)
	}

	// A pattern doesn't match its own fixed folder.
	testenv.Skill(t, filepath.Join(h, "skills/deploy/sub"), "description: Sub")
	cfg = setup(t, h, head+"  - {name: x, match: {global: true}, skills: ['~/skills/deploy', '~/skills/deploy/*', '!~/skills/deploy/*']}\n")
	if len(cfg.Problems) > 0 || !slices.Equal(cfg.Bindings[0].Skills, []string{filepath.Join(h, "skills/deploy")}) {
		t.Errorf("fixed folder: bindings=%v problems=%v", cfg.Bindings, cfg.Problems)
	}

	// A symlink isn't followed: ~/alias/a.md is another path.
	cfg = setup(t, h, head+"  - {name: x, match: {global: true}, instructions: ['~/ctx/*.md', '!~/alias/a.md', '!~/alias/*.md']}\n")
	if len(cfg.Bindings) != 1 || len(cfg.Bindings[0].Instructions) != 2 || len(cfg.Problems) != 2 {
		t.Errorf("symlink: bindings=%v problems=%v", cfg.Bindings, cfg.Problems)
	}

	// An exclusion that removes nothing is reported; the binding applies.
	cfg = setup(t, h, head+"  - {name: x, match: {global: true}, instructions: ['~/ctx/*.md', '!~/ctx/typo.md', '!~/ctx/a.md', '!~/ctx/*.md']}\n")
	if len(cfg.Bindings) != 1 || len(cfg.Bindings[0].Instructions) != 0 || len(cfg.Problems) != 1 || !strings.Contains(cfg.Problems[0], `"!~/ctx/typo.md" excludes nothing`) {
		t.Errorf("bindings=%v problems=%v", cfg.Bindings, cfg.Problems)
	}
}

func TestBadExclusions(t *testing.T) {
	h := testenv.Home(t)
	testenv.Write(t, filepath.Join(h, "a.md"), "a")
	for list, want := range map[string]string{
		`['!~/a.md']`:                       "only entries that start with !",
		`[~/a.md, '!']`:                     "write the path to leave out after it",
		"\n      - ~/a.md\n      - !~/b.md": "quote it: '!~/b.md'",
		`[~/a.md, '!~/[a.md']`:              "invalid glob pattern",
		`[~/a.md, '!ctxtra://nope/a']`:      "unknown source",
		`[~/a.md, '!ctxtra://team/a']`:      "not in ctxtra's store",
		`[~/a.md, '!ctxtra://team/*']`:      "not in ctxtra's store",
		`[~/a.md, '!~/{..,x}/*.md']`:        "cannot traverse parent folders",
	} {
		cfg := load(testenv.Config(t, h, `schema: ctxtra/v1
sources:
  team: {git: https://h/team, branch: main, commit: '`+strings.Repeat("a", 40)+`'}
bindings:
  - name: x
    match: {global: true}
    instructions: `+list+`
`))
		if len(cfg.Bindings) != 0 || len(cfg.Problems) != 1 || !strings.Contains(cfg.Problems[0], want) {
			t.Errorf("%s: bindings=%v problems=%v", list, cfg.Bindings, cfg.Problems)
		}
	}
}

// A pinned source's binding file can exclude only its own files, the same
// as it can add only those.
func TestPinnedExclusions(t *testing.T) {
	h := testenv.Home(t)
	const remote = "https://example.com/team"
	commit := strings.Repeat("a", 40)
	gen := source.GenDir(remote, commit)
	testenv.Write(t, filepath.Join(gen, "a.md"), "a")
	testenv.Write(t, filepath.Join(gen, "b.md"), "b")
	for list, ok := range map[string]bool{
		`['*.md', '!b.md']`:                true,
		`['*.md', '!ctxtra://team/b.md']`:  true,
		`['*.md', '!~/b.md']`:              false,
		`['*.md', '!../b.md']`:             false,
		`['*.md', '!ctxtra://other/b.md']`: false,
		`['*.md', '!{../x,b}.md']`:         false,
	} {
		testenv.Write(t, filepath.Join(gen, "b.yaml"), head+"  - {name: x, match: {global: true}, instructions: "+list+"}\n")
		cfg := load(testenv.Config(t, h, fmt.Sprintf(`schema: ctxtra/v1
sources:
  team: {git: '%s', branch: main, commit: '%s'}
  other: {trusted-path: ~/other}
include: [ctxtra://team/b.yaml]
`, remote, commit)))
		got := len(cfg.Bindings) == 1 && slices.Equal(cfg.Bindings[0].Instructions, []string{filepath.Join(gen, "a.md")})
		if got != ok || ok == (len(cfg.Problems) > 0) {
			t.Errorf("%s: bindings=%v problems=%v", list, cfg.Bindings, cfg.Problems)
		}
	}
}

// A plain path under an imported clone is served from its source, also in an
// exclusion; through a symlink into the clone it is another path.
func TestClonePathExclusions(t *testing.T) {
	h := testenv.Home(t)
	const remote = "https://example.com/team"
	commit := strings.Repeat("a", 40)
	clone := filepath.Join(h, "dev/team")
	testenv.Write(t, filepath.Join(clone, "b.md"), "b")
	gen := source.GenDir(remote, commit)
	testenv.Write(t, filepath.Join(gen, "a.md"), "a")
	testenv.Write(t, filepath.Join(gen, "b.md"), "b")
	if _, err := state.Map(clone, "team", remote); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(clone, "b.md"), filepath.Join(h, "link.md")); err != nil {
		t.Fatal(err)
	}
	for exclude, kept := range map[string][]string{
		"~/dev/team/b.md": {filepath.Join(gen, "a.md")},
		"~/link.md":       {filepath.Join(gen, "a.md"), filepath.Join(gen, "b.md")},
	} {
		cfg := load(testenv.Config(t, h, fmt.Sprintf(`schema: ctxtra/v1
sources:
  team: {git: '%s', branch: main, commit: '%s'}
bindings:
  - {name: x, match: {global: true}, instructions: ['ctxtra://team/*.md', '!%s']}
`, remote, commit, exclude)))
		if len(cfg.Bindings) != 1 || !slices.Equal(cfg.Bindings[0].Instructions, kept) {
			t.Errorf("%s: bindings=%v problems=%v", exclude, cfg.Bindings, cfg.Problems)
		}
	}
}
