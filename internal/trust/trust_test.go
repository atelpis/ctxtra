package trust

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/atelpis/ctxtra/internal/testenv"
)

func symlink(t *testing.T, target, link string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
}

// check fails the test unless Check(path) is nil (want "") or an error that
// contains want.
func check(t *testing.T, p *Policy, path, want string) {
	t.Helper()
	err := p.Check(path)
	switch {
	case want == "" && err != nil:
		t.Errorf("%s: %v", path, err)
	case want != "" && err == nil:
		t.Errorf("%s: trusted, want %q", path, want)
	case want != "" && !strings.Contains(err.Error(), want):
		t.Errorf("%s: %v, want %q", path, err, want)
	}
}

func TestRepositories(t *testing.T) {
	h := testenv.Home(t)
	testenv.Write(t, filepath.Join(h, "notes", "me.md"), "me\n")
	own := testenv.Repo(t, filepath.Join(h, "own"), "")
	team := testenv.Repo(t, filepath.Join(h, "team"), "git@github.com:Acme/Team.git")
	p := New(nil)

	check(t, p, filepath.Join(h, "notes", "me.md"), "")
	check(t, p, filepath.Join(h, "notes", "missing.md"), "")
	check(t, p, filepath.Join(own, "AGENTS.md"), "")
	check(t, p, filepath.Join(team, "AGENTS.md"), "has the remote github.com/acme/team and is not a trusted source")
	check(t, p, filepath.Join(team, "sub", "missing.md"), "has the remote github.com/acme/team")
	check(t, p, "AGENTS.md", "not an absolute path")

	var e *Error
	if err := p.Check(filepath.Join(team, "AGENTS.md")); !errors.As(err, &e) || e.Repo != team {
		t.Errorf("error %#v, want repo %s", err, team)
	}
}

func TestTrustedRoots(t *testing.T) {
	h := testenv.Home(t)
	dot := testenv.Repo(t, filepath.Join(h, "dotfiles"), "git@github.com:me/dotfiles.git")
	evil := testenv.Repo(t, filepath.Join(h, "dotfiles-evil"), "git@github.com:evil/dotfiles.git")
	mono := testenv.Repo(t, filepath.Join(h, "mono"), "git@github.com:acme/mono.git")
	testenv.Write(t, filepath.Join(mono, "docs", "a.md"), "a\n")
	testenv.Write(t, filepath.Join(mono, "src", "b.md"), "b\n")
	// A root given through a symlink is the same folder.
	symlink(t, dot, filepath.Join(h, "link-to-dotfiles"))
	p := New([]Root{{"dotfiles", filepath.Join(h, "link-to-dotfiles")}, {"docs", filepath.Join(mono, "docs")}})

	check(t, p, filepath.Join(dot, "AGENTS.md"), "")
	check(t, p, filepath.Join(h, "link-to-dotfiles", "AGENTS.md"), "")
	check(t, p, filepath.Join(evil, "AGENTS.md"), "github.com/evil/dotfiles")
	check(t, p, filepath.Join(mono, "docs", "a.md"), "")
	check(t, p, filepath.Join(mono, "src", "b.md"), "github.com/acme/mono")
	check(t, p, filepath.Join(mono, "AGENTS.md"), "github.com/acme/mono")
}

// A trusted root covers its own repository. A repository nested below it
// needs its own root, unless it has no remote, and every repository around a
// file must be trusted.
func TestNestedRepositories(t *testing.T) {
	h := testenv.Home(t)
	dot := testenv.Repo(t, filepath.Join(h, "dotfiles"), "git@github.com:me/dotfiles.git")
	vendor := testenv.Repo(t, filepath.Join(dot, "vendor", "team"), "git@github.com:acme/team.git")
	local := testenv.Repo(t, filepath.Join(dot, "local"), "")
	other := testenv.Repo(t, filepath.Join(h, "other"), "git@github.com:acme/other.git")
	inner := testenv.Repo(t, filepath.Join(other, "inner"), "git@github.com:me/inner.git")

	p := New([]Root{{"dotfiles", dot}, {"inner", inner}})
	check(t, p, filepath.Join(vendor, "AGENTS.md"), "github.com/acme/team")
	check(t, p, filepath.Join(local, "AGENTS.md"), "")
	check(t, p, filepath.Join(inner, "AGENTS.md"), "github.com/acme/other")

	p = New([]Root{{"dotfiles", dot}, {"team", vendor}})
	check(t, p, filepath.Join(vendor, "AGENTS.md"), "")
}

// A worktree has a .git file that points to the main repository's git
// folder; the remote is in the shared config. A worktree in another folder
// is not covered by the main checkout's root.
func TestWorktree(t *testing.T) {
	h := testenv.Home(t)
	dot := testenv.Repo(t, filepath.Join(h, "dotfiles"), "git@github.com:me/dotfiles.git")
	wt := filepath.Join(h, "wt")
	testenv.Run(t, dot, "git", "worktree", "add", "-q", wt)

	check(t, New([]Root{{"dotfiles", dot}}), filepath.Join(wt, "AGENTS.md"), "github.com/me/dotfiles")
	check(t, New([]Root{{"wt", wt}}), filepath.Join(wt, "AGENTS.md"), "")
}

func TestSymlinks(t *testing.T) {
	h := testenv.Home(t)
	team := testenv.Repo(t, filepath.Join(h, "team"), "git@github.com:acme/team.git")
	testenv.Write(t, filepath.Join(h, "secret"), "s\n")
	testenv.Write(t, filepath.Join(h, "notes", "x.md"), "x\n")
	p := New(nil)

	// A link outside git to a file in an untrusted repository.
	symlink(t, filepath.Join(team, "AGENTS.md"), filepath.Join(h, "notes", "team.md"))
	check(t, p, filepath.Join(h, "notes", "team.md"), "leads through a symlink to "+"~/team/AGENTS.md")
	// A link in an untrusted repository to a trusted file.
	symlink(t, filepath.Join(h, "secret"), filepath.Join(team, "secret.md"))
	check(t, p, filepath.Join(team, "secret.md"), "github.com/acme/team")
	// A folder link in an untrusted repository, and a relative link.
	symlink(t, filepath.Join(h, "notes"), filepath.Join(team, "notes"))
	check(t, p, filepath.Join(team, "notes", "x.md"), "github.com/acme/team")
	symlink(t, "../team/AGENTS.md", filepath.Join(h, "notes", "rel.md"))
	check(t, p, filepath.Join(h, "notes", "rel.md"), "github.com/acme/team")
	// Links that stay outside git are fine.
	symlink(t, "x.md", filepath.Join(h, "notes", "y.md"))
	check(t, p, filepath.Join(h, "notes", "y.md"), "")
	// A loop.
	symlink(t, "loop2", filepath.Join(h, "notes", "loop1"))
	symlink(t, "loop1", filepath.Join(h, "notes", "loop2"))
	check(t, p, filepath.Join(h, "notes", "loop1"), "too many symlinks")
}

// Anything that can't be read counts as untrusted.
func TestUnreadableRepository(t *testing.T) {
	h := testenv.Home(t)
	own := testenv.Repo(t, filepath.Join(h, "own"), "")
	f, err := os.OpenFile(filepath.Join(own, ".git", "config"), os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	f.WriteString("[include]\n\tpath = remotes.inc\n")
	f.Close()
	check(t, New(nil), filepath.Join(own, "AGENTS.md"), "which ctxtra can't check (")

	testenv.Write(t, filepath.Join(h, "broken", ".git"), "not a gitdir line\n")
	check(t, New(nil), filepath.Join(h, "broken", "a.md"), "can't check")
	testenv.Write(t, filepath.Join(h, "gone", ".git"), "gitdir: /nonexistent\n")
	check(t, New(nil), filepath.Join(h, "gone", "a.md"), "can't check")
}

func TestParseRemotes(t *testing.T) {
	cases := []struct {
		config, url string
		has, err    bool
	}{
		{"[core]\n\tbare = false\n", "", false, false},
		{"[remote \"origin\"]\n\turl = https://GitHub.com/a/b.git\n", "github.com/a/b", true, false},
		{"[Remote \"x\"]\nURL=\"git@h:a/b\"\n", "h/a/b", true, false},
		{"[remote.old]\n", "(unknown)", true, false},
		{"  [ remote \"y\" ] url = h:c/d\n", "h/c/d", true, false},
		{"[remote \"a]b\\\"c\"]\n\turl = h:a/b # comment\n", "h/a/b", true, false},
		{"[core]\n\tbare\n\t; comment\n[branch \"main\"] remote = origin\n", "", false, false},
		{"\ufeff[remote \"origin\"]\n\turl = h:a/b\n", "h/a/b", true, false},
		{"[includeIf \"gitdir:~/x/\"]\npath = y\n", "", false, true},
		{"\ufeff[include]\npath = y\n", "", false, true},
		{"[remote \"origin\"\n", "", false, true},
		{"this is not valid git config\n", "", false, true},
		{"url = h:a/b\n", "", false, true},
		{"[core]\n\tx = a \\\n[remote \"y\"]\n", "", false, true},
		{"[core]\n\t9x = 1\n", "", false, true},
		{"[core]\n\tx = \"unterminated\n", "", false, true},
		{"[core]\n\tx = a\\qb\n", "", false, true},
		{"[core]\n\tx = \"a # b\" \\\"c\\\" ; \"open\n", "", false, false},
	}
	for _, c := range cases {
		url, has, err := parseRemotes([]byte(c.config))
		if url != c.url || has != c.has || (err != nil) != c.err {
			t.Errorf("%q: %q %v %v", c.config, url, has, err)
		}
	}
}

// A submodule's .git is a file that points into the superproject's git
// folder, where the submodule's own config is.
func TestSubmodule(t *testing.T) {
	h := testenv.Home(t)
	team := testenv.Repo(t, filepath.Join(h, "team"), "")
	dot := testenv.Repo(t, filepath.Join(h, "dotfiles"), "git@github.com:me/dotfiles.git")
	testenv.Run(t, dot, "git", "-c", "protocol.file.allow=always", "submodule", "add", "-q", team, "vendor/team")
	sub := filepath.Join(dot, "vendor", "team", "AGENTS.md")
	check(t, New([]Root{{"dotfiles", dot}}), sub, "is in ~/dotfiles/vendor/team, which has the remote ")
	check(t, New([]Root{{"dotfiles", dot}, {"team", filepath.Join(dot, "vendor", "team")}}), sub, "")
}

// Git metadata reads have a budget for the whole Policy; over it, a
// repository counts as untrusted.
func TestGitBudget(t *testing.T) {
	h := testenv.Home(t)
	var files []string
	for _, n := range []string{"a", "b", "c", "d", "e"} {
		r := testenv.Repo(t, filepath.Join(h, n), "")
		f, err := os.OpenFile(filepath.Join(r, ".git", "config"), os.O_APPEND|os.O_WRONLY, 0)
		if err != nil {
			t.Fatal(err)
		}
		f.WriteString("# " + strings.Repeat("x", 1000*1000) + "\n")
		f.Close()
		files = append(files, filepath.Join(r, "AGENTS.md"))
	}
	p := New(nil)
	for _, f := range files[:4] {
		check(t, p, f, "")
	}
	check(t, p, files[4], "too much git metadata")
}

// Oversized configs count against the budget too.
func TestGitBudgetOversized(t *testing.T) {
	h := testenv.Home(t)
	p := New(nil)
	for i, n := range []string{"a", "b", "c", "d"} {
		r := testenv.Repo(t, filepath.Join(h, n), "")
		testenv.Write(t, filepath.Join(r, ".git", "config"), strings.Repeat("#", 2<<20))
		want := "larger than"
		if i == 3 { // three reads of 1 MiB + 1 byte leave less than 1 MiB
			want = "too much git metadata"
		}
		check(t, p, filepath.Join(r, "AGENTS.md"), want)
	}
}
