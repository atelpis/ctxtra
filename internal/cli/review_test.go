package cli

import (
	"strings"
	"testing"

	"github.com/atelpis/ctxtra/internal/config"
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
