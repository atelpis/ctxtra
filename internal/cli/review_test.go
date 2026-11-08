package cli

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/ctxtra/ctxtra/internal/config"
)

// Match blocks are compared as data, not by how they print: a remote pattern
// named "every session" prints like global: true.
func TestSelectorWarnings(t *testing.T) {
	cfg := func(sel config.Selector) *config.Config {
		return &config.Config{Bindings: []config.Binding{{Name: "b", Source: "/gen/b.yaml", Selector: sel}}}
	}
	cases := []struct {
		old, new config.Selector
		want     string
	}{
		{config.Selector{GitRepos: []config.GitRepo{{Remote: "every session"}}}, config.Selector{Global: true}, "now applies to every session"},
		{config.Selector{GitRepos: []config.GitRepo{{Remote: "a, b"}}}, config.Selector{GitRepos: []config.GitRepo{{Remote: "a"}, {Remote: "b"}}}, "match changed"},
		{config.Selector{Dirs: []string{"/x"}}, config.Selector{Dirs: []string{"/x"}}, ""},
	}
	for _, c := range cases {
		var out strings.Builder
		s := &sources{IO: IO{Out: &out}}
		s.selectorWarnings(config.Source{}, cfg(c.old), cfg(c.new), "/gen", "/gen")
		if c.want == "" && out.Len() > 0 || !strings.Contains(out.String(), c.want) {
			t.Errorf("%v -> %v: %q", c.old, c.new, out.String())
		}
	}
}

// Added lines are numbered from the hunk headers. A content line that looks
// like a header stays content, an empty line is context (as with
// diff.suppressBlankEmpty), and a quoted path is unquoted.
func TestParseDiff(t *testing.T) {
	d := `diff --git a/a.md b/a.md
index 1..2 100644
--- a/a.md
+++ b/a.md
@@ -1,3 +1,4 @@
 one

-old
+++ b/fake.md
+new
\ No newline at end of file
diff --git "a/caf\303\251 x.md" "b/caf\303\251 x.md"
--- /dev/null
+++ "b/caf\303\251 x.md"	
@@ -0,0 +1 @@
+first
diff --git a/gone.md b/gone.md
--- a/gone.md
+++ /dev/null
@@ -1 +0,0 @@
-bye
`
	var got []string
	walkDiff(d, func(l diffLine) {
		if l.added {
			got = append(got, fmt.Sprintf("%s:%d", l.path, l.line))
		}
	})
	want := []string{"a.md:3", "a.md:4", "café x.md:1"}
	if !slices.Equal(got, want) {
		t.Errorf("added lines %q, want %q", got, want)
	}
}

func TestSpans(t *testing.T) {
	var s spans
	for _, n := range []int{1, 2, 3, 7, 9, 10} {
		s.add(n)
	}
	for n, want := range map[int]bool{0: false, 1: true, 3: true, 4: false, 7: true, 8: false, 10: true, 11: false} {
		if s.has(n) != want {
			t.Errorf("has(%d) = %v; spans %v", n, !want, s)
		}
	}
}
