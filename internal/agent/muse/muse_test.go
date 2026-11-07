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
	res := &resolve.Result{Notices: []string{"source needs checking"}, Skills: []resolve.Hit{{Name: "deploy", Source: "team"}}}
	for i := 0; i < 16; i++ {
		res.Files = append(res.Files, resolve.Hit{Path: "/notes/" + strings.Repeat("a", 140) + ".md"})
	}
	res.Files = append(res.Files, resolve.Hit{Source: "team", SourcePath: "short.md"})
	a := Agent{}
	for _, answer := range []any{a.Answer("SessionStart", res), a.SlotAnswer("SessionStart", "context", agent.Summary(res))} {
		got := answer.(map[string]any)["systemMessage"].(string)
		b, _ := jsonorder.Marshal(got)
		if len(b)-2 > maxSummary || !strings.Contains(got, "\n  team\n  - short.md\nSkills:\n  team\n  - deploy\n"+agent.FullList+"\n\nℹ source needs checking") ||
			!strings.Contains(got, " more\n  team\n") || strings.HasSuffix(got, "…") {
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
		res.Files = append(res.Files, resolve.Hit{Source: "notes", SourcePath: strings.Repeat("a", 140) + ".md"})
	}
	res.Files = append(res.Files, resolve.Hit{Source: "notes", SourcePath: "+100 more"})
	got := clip(agent.Summary(res))
	if !strings.Contains(got, "\n  - \"+100 more\"") {
		t.Fatalf("lost literal filename: %q", got)
	}
	listed, missing := 0, 0
	for _, line := range strings.Split(got, "\n") {
		if n, ok := omitted(line); ok {
			missing += n
		} else if strings.HasPrefix(line, agent.EntryPrefix) {
			listed++
		}
	}
	if listed+missing != 15 || missing == 0 {
		t.Fatalf("listed %d, omitted %d: %q", listed, missing, got)
	}
}

// Many groups with long names: whole groups are omitted, counted in one line
// per list, before the end is cut, so the notices stay.
func TestManyLongGroups(t *testing.T) {
	res := &resolve.Result{Problems: []string{"p"}, Notices: []string{"team has new commits on main"}}
	for i := range 20 {
		src := fmt.Sprintf("%02d-%s", i, strings.Repeat("s", 100))
		res.Files = append(res.Files, resolve.Hit{Source: src, SourcePath: "a.md"}, resolve.Hit{Source: src, SourcePath: "b.md"})
		res.Skills = append(res.Skills, resolve.Hit{Source: src, Name: "deploy"})
	}
	got := clip(agent.Summary(res))
	b, _ := jsonorder.Marshal(got)
	if len(b)-2 > maxSummary || strings.HasSuffix(got, "…") || !strings.Contains(got, "; 1 problem (run ctxtra doctor)\n") ||
		!strings.HasSuffix(got, "\n"+agent.FullList+"\n\nℹ team has new commits on main") {
		t.Fatalf("lost summary information (%d JSON bytes): %q", len(b)-2, got)
	}
	// Every entry is still counted, in its list.
	counts := map[string]int{}
	section := ""
	for _, line := range strings.Split(got, "\n") {
		switch {
		case line == "Instructions:" || line == "Skills:":
			section = line
		case strings.HasPrefix(line, agent.EntryPrefix):
			n, ok := omitted(line)
			if !ok {
				n = 1
			}
			counts[section] += n
		default:
			if n, _, ok := omittedGroups(line); ok {
				counts[section] += n
			}
		}
	}
	if counts["Instructions:"] != 40 || counts["Skills:"] != 20 || !strings.Contains(got, " more from ") {
		t.Errorf("counts %v: %q", counts, got)
	}
}

func TestOmitGroup(t *testing.T) {
	lines := strings.Split("✓ x\nInstructions:\n  aa\n  - a.md\n  - +2 more\n  b\n  - b.md\nSkills:\n  c\n  - c\n\nℹ n", "\n")
	got, ok := omitGroup(lines)
	want := "✓ x\nInstructions:\n  b\n  - b.md\n  +3 more from 1 source\nSkills:\n  c\n  - c\n\nℹ n"
	if !ok || strings.Join(got, "\n") != want {
		t.Fatalf("first: %q", strings.Join(got, "\n"))
	}
	got, _ = omitGroup(got)
	if want := "✓ x\nInstructions:\n  +4 more from 2 sources\nSkills:\n  c\n  - c\n\nℹ n"; strings.Join(got, "\n") != want {
		t.Fatalf("second: %q", strings.Join(got, "\n"))
	}
}
