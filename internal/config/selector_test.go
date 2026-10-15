package config

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/atelpis/ctxtra/internal/gitrepo"
	"github.com/atelpis/ctxtra/internal/testenv"
)

func TestSelectorMatch(t *testing.T) {
	h := testenv.Home(t)
	r := testenv.Repo(t, filepath.Join(h, "dev", "work", "mono"), "git@github.com:Acme/Mono.git")
	wt := filepath.Join(h, "worktrees", "mono-feature")
	testenv.Run(t, r, "git", "worktree", "add", "-q", wt, "-b", "feature")
	billing := filepath.Join(wt, "services", "billing", "api")
	plain := filepath.Join(h, "scratch", "notes")
	for _, d := range []string{billing, plain} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	sel := map[string]Selector{
		"global":      {Global: true},
		"work dirs":   {Dirs: []string{h + "/dev/work/**"}},
		"scratch":     {Dirs: []string{h + "/scratch/**"}},
		"exact dir":   {Dirs: []string{h + "/scratch"}},
		"acme":        {GitRepos: []GitRepo{{Remote: "github.com/acme/*"}}},
		"billing":     {GitRepos: []GitRepo{{Remote: "github.com/acme/mono", Subpath: "services/billing/**"}}},
		"other":       {GitRepos: []GitRepo{{Remote: "github.com/other/*"}}},
		"either (OR)": {Dirs: []string{"/nowhere/**"}, GitRepos: []GitRepo{{Remote: "github.com/nope/x"}, {Remote: "github.com/acme/mono"}}},
	}
	want := map[string][]string{
		r:       {"global", "work dirs", "acme", "either (OR)"},
		wt:      {"global", "work dirs", "acme", "either (OR)"}, // by the main checkout
		billing: {"global", "work dirs", "acme", "billing", "either (OR)"},
		plain:   {"global", "scratch"},
	}
	for dir, names := range want {
		repo := gitrepo.Identify(dir)
		for name, s := range sel {
			got, why := s.Match(dir, repo)
			if got != contains(names, name) {
				t.Errorf("%s: selector %q = %v (%s)", dir, name, got, why)
			}
		}
	}
}

func contains(list []string, s string) bool {
	return slices.Contains(list, s)
}

// A dirs entry without glob characters whose last part is a symlink matches
// the folder the symlink points to (/tmp on macOS is a symlink).
func TestExactDirThroughSymlink(t *testing.T) {
	h := testenv.Home(t)
	real := filepath.Join(h, "real")
	if err := os.Mkdir(real, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(h, "link")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	for _, pat := range []string{link, link + "/**"} {
		if ok, why := (Selector{Dirs: []string{pat}}).Match(real, nil); !ok {
			t.Errorf("%s: %s", pat, why)
		}
	}
}
