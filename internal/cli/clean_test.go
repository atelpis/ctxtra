package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/atelpis/ctxtra/internal/source"
	"github.com/atelpis/ctxtra/internal/state"
	"github.com/atelpis/ctxtra/internal/testenv"
)

// Cleaning the store keeps the accepted commits of every config that add or
// sync changed, not only the current one.
func TestCleanStoreKeepsOtherConfigs(t *testing.T) {
	h := testenv.Home(t)
	url := "https://h/team"
	a, b, c := strings.Repeat("a", 40), strings.Repeat("b", 40), strings.Repeat("c", 40)
	old := time.Now().Add(-40 * 24 * time.Hour)
	for _, commit := range []string{a, b, c} {
		g := source.GenDir(url, commit)
		testenv.Write(t, filepath.Join(g, "x.md"), "x")
		os.Chtimes(g, old, old)
	}
	cfg := func(name, commit string) string {
		p := filepath.Join(h, name)
		testenv.Write(t, p, "schema: ctxtra/v1\nsources:\n  team: {git: '"+url+"', branch: main, commit: '"+commit+"'}\n")
		return p
	}
	mine, other := cfg("mine.yaml", a), cfg("other.yaml", b)
	if err := state.RecordConfig(other); err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	s := &sources{IO: IO{Out: &out, Err: &out}, file: mine}
	s.cleanStore()
	for commit, want := range map[string]bool{a: true, b: true, c: false} {
		if source.Has(url, commit) != want {
			t.Errorf("%s in the store: %v, want %v (%s)", commit[:1], !want, want, out.String())
		}
	}
	// A recorded config that can't be read stops the cleaning: it may pin
	// anything.
	os.WriteFile(other, []byte("kind: ["), 0o600)
	out.Reset()
	s.cleanStore()
	if !source.Has(url, b) || !strings.Contains(out.String(), "the store is not cleaned") {
		t.Errorf("unreadable config: %s", out.String())
	}
}
