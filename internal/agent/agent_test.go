package agent_test

import (
	"testing"

	"github.com/atelpis/ctxtra/internal/agent"
	"github.com/atelpis/ctxtra/internal/resolve"
)

func TestSummary(t *testing.T) {
	files := func(names ...string) []resolve.Hit {
		var out []resolve.Hit
		for _, n := range names {
			out = append(out, resolve.Hit{Path: "/notes/" + n})
		}
		return out
	}
	skills := []resolve.Hit{{Name: "deploy"}, {Name: "review"}}
	for want, res := range map[string]*resolve.Result{
		"ctxtra: added 2 files (a.md, b.md)":                        {Files: files("a.md", "b.md")},
		"ctxtra: added 1 file (a.md) and 2 skills (deploy, review)": {Files: files("a.md"), Skills: skills},
		"ctxtra: added 5 files (a.md, b.md, c.md, +2 more)":         {Files: files("a.md", "b.md", "c.md", "d.md", "e.md")},
		"ctxtra: added 1 file (a.md); 1 not found, 2 over budget, 1 problem (run ctxtra doctor)": {
			Files: files("a.md"), Missing: []string{"x"}, Dropped: files("b.md", "c.md"), Problems: []string{"p"}},
		"ctxtra: added nothing; 2 problems (run ctxtra doctor)": {Problems: []string{"p", "q"}},
		"ctxtra: added 1 file (a.md)\nctxtra: team last checked 9 days ago\nctxtra: n2": {
			Files: files("a.md"), Notices: []string{"team last checked 9 days ago", "n2"}},
	} {
		if got := agent.Summary(res); got != want {
			t.Errorf("got  %q\nwant %q", got, want)
		}
	}
	if agent.Summary(nil) != "" || agent.Summary(&resolve.Result{}) != "" {
		t.Error("summary when nothing was added")
	}
}
