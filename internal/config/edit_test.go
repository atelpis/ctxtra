package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/atelpis/ctxtra/internal/testenv"
)

const commitA = "9f8e7d6c5b4a39281706f5e4d3c2b1a098765432"
const commitB = "1111111111111111111111111111111111111111"

const commented = `schema: ctxtra/v1

# my sources
sources:
  dotfiles:   {trusted-path: ~/dev/dotfiles}   # live
  team-ctx:
    git: git@github.com:acme/team-ctx.git
    branch: main                     # what sync checks
    commit: '` + commitA + `'   # accepted

bindings:                            # my own
  - name: me
    match: {global: true}
    instructions: [me.md]

include:                             # binding files
  - bindings.yaml                    # plain

budget:
  max-chars: 40000
`

// Text edits keep everything else byte for byte.
func TestTextEdits(t *testing.T) {
	out, err := SetCommit([]byte(commented), "team-ctx", commitB)
	if err != nil || string(out) != strings.Replace(commented, commitA, commitB, 1) {
		t.Errorf("SetCommit %v:\n%s", err, out)
	}
	out, err = AddInclude([]byte(commented), "ctxtra://team-ctx/b.yaml")
	if err != nil || string(out) != strings.Replace(commented, "# plain\n", "# plain\n  - ctxtra://team-ctx/b.yaml\n", 1) {
		t.Errorf("AddInclude %v:\n%s", err, out)
	}
	out, err = AddSource([]byte(commented), "globex", [][2]string{{"git", "https://h/x.git"}, {"branch", "main"}, {"commit", commitB}})
	want := strings.Replace(commented, "   # accepted\n", "   # accepted\n  globex:\n    git: https://h/x.git\n    branch: main\n    commit: '"+commitB+"'\n", 1)
	if err != nil || string(out) != want {
		t.Errorf("AddSource %v:\n%s", err, out)
	}
	if _, err := AddSource([]byte(commented), "dotfiles", [][2]string{{"trusted-path", "/x"}}); err == nil {
		t.Error("AddSource: an existing name was accepted")
	}
	out, err = AddGlobalSkill([]byte(commented), "ctxtra", "~/.ctxtra/skills/ctxtra")
	want = strings.Replace(commented, "[me.md]\n", "[me.md]\n  - name: ctxtra\n    match:\n      global: true\n    skills:\n      - ~/.ctxtra/skills/ctxtra\n", 1)
	if err != nil || string(out) != want {
		t.Errorf("AddGlobalSkill %v:\n%s", err, out)
	}
	if _, err := AddGlobalSkill([]byte(commented), "me", "/x"); err == nil {
		t.Error("AddGlobalSkill: an existing name was accepted")
	}
}

func TestEditsOnOtherShapes(t *testing.T) {
	cases := []struct {
		name, in, want string
		edit           func([]byte) ([]byte, error)
	}{
		{"include into a flow list", "schema: ctxtra/v1\ninclude: [a.yaml, 'b c.yaml']  # x\n",
			"schema: ctxtra/v1\ninclude: [a.yaml, 'b c.yaml', '#x.yaml']  # x\n",
			func(d []byte) ([]byte, error) { return AddInclude(d, "#x.yaml") }},
		{"first include", "schema: ctxtra/v1", "schema: ctxtra/v1\ninclude:\n  - a.yaml\n",
			func(d []byte) ([]byte, error) { return AddInclude(d, "a.yaml") }},
		{"first include after own bindings", "schema: ctxtra/v1\nbindings:\n  - name: me\n    match: {global: true}\n    instructions: [me.md]\n",
			"schema: ctxtra/v1\nbindings:\n  - name: me\n    match: {global: true}\n    instructions: [me.md]\ninclude:\n  - a.yaml\n",
			func(d []byte) ([]byte, error) { return AddInclude(d, "a.yaml") }},
		{"new config", "", NewConfig + "sources:\n  x:\n    trusted-path: ~/x\n",
			func(d []byte) ([]byte, error) { return AddSource(d, "x", [][2]string{{"trusted-path", "~/x"}}) }},
		{"commit of a handwritten source", "schema: ctxtra/v1\nsources:\n  t:\n      git: https://h/t\n      branch: main\n",
			"schema: ctxtra/v1\nsources:\n  t:\n      git: https://h/t\n      branch: main\n      commit: '" + commitB + "'\n",
			func(d []byte) ([]byte, error) { return SetCommit(d, "t", commitB) }},
		{"first binding", "schema: ctxtra/v1\ninclude: [a.yaml]\n",
			"schema: ctxtra/v1\ninclude: [a.yaml]\nbindings:\n  - name: s\n    match:\n      global: true\n    skills:\n      - ~/s\n",
			func(d []byte) ([]byte, error) { return AddGlobalSkill(d, "s", "~/s") }},
		{"binding after an unindented list", "schema: ctxtra/v1\nbindings:\n- name: me\n  match:\n    global: true\n  instructions: [me.md]\n",
			"schema: ctxtra/v1\nbindings:\n- name: me\n  match:\n    global: true\n  instructions: [me.md]\n- name: s\n  match:\n    global: true\n  skills:\n    - ~/s\n",
			func(d []byte) ([]byte, error) { return AddGlobalSkill(d, "s", "~/s") }},
		// Flow maps are encoded again: the comment stays, the layout doesn't.
		{"flow map", "schema: ctxtra/v1\nsources: {t: {git: 'https://h/t', branch: main}}  # c\n",
			"schema: ctxtra/v1\nsources: {t: {git: 'https://h/t', branch: main, commit: '" + commitB + "'}} # c\n",
			func(d []byte) ([]byte, error) { return SetCommit(d, "t", commitB) }},
	}
	for _, c := range cases {
		out, err := c.edit([]byte(c.in))
		if err != nil || string(out) != c.want {
			t.Errorf("%s: %v\n%s\nwant:\n%s", c.name, err, out, c.want)
		}
		if err == nil {
			if err := Check(out); err != nil {
				t.Errorf("%s: invalid result: %v", c.name, err)
			}
		}
	}
}

func TestEdit(t *testing.T) {
	h := testenv.Home(t)
	real := filepath.Join(h, "dotfiles", "config.yaml")
	testenv.Write(t, real, "schema: ctxtra/v1\n")
	os.Chmod(real, 0o640)
	link := filepath.Join(h, ".config", "ctxtra", "config.yaml")
	os.MkdirAll(filepath.Dir(link), 0o755)
	os.Symlink(real, link)

	err := Edit(link, func(old []byte) ([]byte, error) { return AddInclude(old, "a.yaml") })
	if err != nil {
		t.Fatal(err)
	}
	if st, err := os.Lstat(link); err != nil || st.Mode()&os.ModeSymlink == 0 {
		t.Error("the symlink was replaced")
	}
	if b, _ := os.ReadFile(real); !strings.HasSuffix(string(b), "include:\n  - a.yaml\n") {
		t.Errorf("content:\n%s", b)
	}
	if st, _ := os.Stat(real); st.Mode().Perm() != 0o640 {
		t.Errorf("mode %v", st.Mode())
	}
	if b, _ := os.ReadDir(filepath.Join(h, ".ctxtra", "backups")); len(b) != 1 {
		t.Errorf("backups: %v", b)
	}
	// An edit that makes the config invalid changes nothing.
	err = Edit(link, func(old []byte) ([]byte, error) { return append(old, "rules: []\n"...), nil })
	if err == nil || !strings.Contains(err.Error(), "nothing changed") {
		t.Errorf("invalid edit: %v", err)
	}
}

// A config that isn't valid is never edited: a second document would be
// lost.
func TestEditRefusesInvalidConfig(t *testing.T) {
	h := testenv.Home(t)
	in := "schema: ctxtra/v1\n---\nschema: ctxtra/v1\ninclude: [x.yaml]\n"
	p := testenv.Config(t, h, in)
	err := Edit(p, func(old []byte) ([]byte, error) { return AddSource(old, "x", [][2]string{{"trusted-path", "/x"}}) })
	if err == nil || !strings.Contains(err.Error(), "fix it first") {
		t.Errorf("edit: %v", err)
	}
	if b, _ := os.ReadFile(p); string(b) != in {
		t.Errorf("changed:\n%s", b)
	}
	if _, err := AddSource([]byte(in), "x", [][2]string{{"trusted-path", "/x"}}); err == nil {
		t.Error("AddSource accepted two documents")
	}
	if _, err := Parse([]byte(in)); err == nil {
		t.Error("Parse accepted two documents")
	}
}

func TestRemoveAndReplace(t *testing.T) {
	in := `schema: ctxtra/v1
sources:
  dotfiles: {trusted-path: ~/dev/dotfiles}   # live
  team:
    git: https://h/team
    branch: main
    commit: '` + commitA + `'
include:
  - bindings.yaml        # mine
  - ctxtra://team/b.yaml
`
	cases := []struct {
		name, want string
		edit       func([]byte) ([]byte, error)
	}{
		{"remove an include", strings.Replace(in, "  - ctxtra://team/b.yaml\n", "", 1),
			func(d []byte) ([]byte, error) { return RemoveInclude(d, "ctxtra://team/b.yaml") }},
		{"remove a source", strings.Replace(in, "  team:\n    git: https://h/team\n    branch: main\n    commit: '"+commitA+"'\n", "", 1),
			func(d []byte) ([]byte, error) { return RemoveSource(d, "team") }},
		{"remove a flow source", strings.Replace(in, "  dotfiles: {trusted-path: ~/dev/dotfiles}   # live\n", "", 1),
			func(d []byte) ([]byte, error) { return RemoveSource(d, "dotfiles") }},
		{"replace a source", strings.Replace(in, "    git: https://h/team\n    branch: main\n    commit: '"+commitA+"'\n", "    trusted-path: ~/dev/team\n", 1),
			func(d []byte) ([]byte, error) {
				return ReplaceSource(d, "team", [][2]string{{"trusted-path", "~/dev/team"}})
			}},
	}
	for _, c := range cases {
		out, err := c.edit([]byte(in))
		if err != nil || string(out) != c.want {
			t.Errorf("%s: %v\n%s", c.name, err, out)
		}
	}
	// The last entry takes its key with it.
	one := "schema: ctxtra/v1\nsources:\n  a: {trusted-path: /a}\ninclude:\n  - x.yaml\n"
	out, _ := RemoveInclude([]byte(one), "x.yaml")
	out, _ = RemoveSource(out, "a")
	if string(out) != "schema: ctxtra/v1\n" {
		t.Errorf("last entries:\n%s", out)
	}
	if _, err := RemoveInclude([]byte(one), "y.yaml"); err == nil {
		t.Error("removing a missing include")
	}
	// A flow list is encoded again.
	out, err := RemoveInclude([]byte("schema: ctxtra/v1\ninclude: [a.yaml, b.yaml]\n"), "a.yaml")
	if err != nil || string(out) != "schema: ctxtra/v1\ninclude: [b.yaml]\n" {
		t.Errorf("flow list: %v\n%s", err, out)
	}
}
