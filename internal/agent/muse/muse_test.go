package muse

import (
	"fmt"
	"strings"
	"testing"

	"github.com/atelpis/ctxtra/internal/agent"
	"github.com/atelpis/ctxtra/internal/jsonorder"
	"github.com/atelpis/ctxtra/internal/resolve"
)

// A long summary is cut, so the answer stays within what Muse takes.
func TestClip(t *testing.T) {
	if got := clip("ctxtra: added 1 file (a.md)"); got != "ctxtra: added 1 file (a.md)" {
		t.Errorf("short summary: %q", got)
	}
	long := strings.Repeat("ctxtra: \"source\" last checked 9 days ago; run ctxtra source update\n", 100)
	got := clip(long)
	if b, _ := jsonorder.Marshal(got); len(b)-2 > maxSummary || !strings.HasSuffix(got, "…") || !strings.HasPrefix(long, strings.TrimSuffix(got, "…")) {
		t.Errorf("%d bytes of JSON: %q", len(b)-2, got)
	}
}

func TestLongSummaryLists(t *testing.T) {
	res := &resolve.Result{Notices: []string{"source needs checking"}, Skills: []resolve.Hit{{Name: "deploy"}}}
	for i := 0; i < 16; i++ {
		res.Files = append(res.Files, resolve.Hit{Path: "/notes/" + strings.Repeat("a", 140) + ".md"})
	}
	a := Agent{}
	for _, answer := range []any{a.Answer("SessionStart", res), a.SlotAnswer("SessionStart", "context", agent.Summary(res))} {
		got := answer.(map[string]any)["systemMessage"].(string)
		b, _ := jsonorder.Marshal(got)
		if len(b)-2 > maxSummary || !strings.Contains(got, "Skills:\n- deploy") || !strings.Contains(got, "\n\nℹ source needs checking") || !strings.Contains(got, " more\nSkills:") || strings.HasSuffix(got, "…") {
			t.Fatalf("lost summary information (%d JSON bytes): %q", len(b)-2, got)
		}
	}
}

// Long paths of same-named files don't hide the problems: those in a list
// are omitted first, and dropped ones come after the count of problems.
func TestLongPathsKeepProblems(t *testing.T) {
	long := func(i int) resolve.Hit {
		return resolve.Hit{Path: fmt.Sprintf("/%d/%s/style.md", i, strings.Repeat("é", 400)), Over: "budget.max-chars"}
	}
	res := &resolve.Result{Problems: []string{"p"}, Notices: []string{"source needs checking"}}
	for i := range 15 {
		h := long(i)
		h.Over = ""
		res.Files = append(res.Files, h)
	}
	if got := clip(agent.Summary(res)); !strings.Contains(got, "1 problem (run ctxtra doctor)") || !strings.Contains(got, "\n\nℹ source needs checking") {
		t.Errorf("long list: %q", got)
	}
	res.Dropped = []resolve.Hit{long(20), long(21), long(22)}
	got := clip(agent.Summary(res))
	if b, _ := jsonorder.Marshal(got); len(b)-2 > maxSummary || !strings.Contains(got, "; 1 problem, dropped /20/") {
		t.Errorf("long dropped names (%d JSON bytes): %q", len(b)-2, got)
	}
}

func TestLiteralOverflowName(t *testing.T) {
	res := &resolve.Result{}
	for i := 0; i < 14; i++ {
		res.Files = append(res.Files, resolve.Hit{Path: "/notes/" + strings.Repeat("a", 140) + ".md"})
	}
	res.Files = append(res.Files, resolve.Hit{Path: "/notes/+100 more"})
	got := clip(agent.Summary(res))
	if !strings.Contains(got, "\n- \"+100 more\"") {
		t.Fatalf("lost literal filename: %q", got)
	}
	listed, missing := 0, 0
	for _, line := range strings.Split(got, "\n") {
		if n, ok := omitted(line); ok {
			missing += n
		} else if strings.HasPrefix(line, "- ") {
			listed++
		}
	}
	if listed+missing != 15 || missing == 0 {
		t.Fatalf("listed %d, omitted %d: %q", listed, missing, got)
	}
}
