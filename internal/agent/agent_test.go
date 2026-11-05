package agent_test

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/atelpis/ctxtra/internal/agent"
	"github.com/atelpis/ctxtra/internal/chunk"
	"github.com/atelpis/ctxtra/internal/jsonorder"
	"github.com/atelpis/ctxtra/internal/resolve"
	"github.com/atelpis/ctxtra/internal/source"
	"github.com/atelpis/ctxtra/internal/testenv"
)

func TestSummary(t *testing.T) {
	files := func(names ...string) []resolve.Hit {
		var out []resolve.Hit
		for _, n := range names {
			out = append(out, resolve.Hit{Path: "/notes/" + n})
		}
		return out
	}
	over := func(hits []resolve.Hit, limit string) []resolve.Hit {
		for i := range hits {
			hits[i].Over = limit
		}
		return hits
	}
	skills := []resolve.Hit{{Name: "deploy"}, {Name: "review"}}
	for want, res := range map[string]*resolve.Result{
		"✓ ctxtra added 2 instructions\nInstructions:\n- a.md\n- b.md":                                  {Files: files("a.md", "b.md")},
		"✓ ctxtra added 1 instruction and 2 skills\nInstructions:\n- a.md\nSkills:\n- deploy\n- review": {Files: files("a.md"), Skills: skills},
		"✓ ctxtra added 5 instructions\nInstructions:\n- a.md\n- b.md\n- c.md\n- d.md\n- e.md":          {Files: files("a.md", "b.md", "c.md", "d.md", "e.md")},
		"✓ ctxtra added 1 instruction; 1 not found, 1 problem, dropped b.md, c.md (over budget.max-chars) (run ctxtra doctor)\nInstructions:\n- a.md": {
			Files: files("a.md"), Missing: []string{"x"}, Dropped: over(files("b.md", "c.md"), "budget.max-chars"), Problems: []string{"p"}},
		"✓ ctxtra added 1 instruction; dropped b.md, c.md, d.md, +1 more (over Claude Code's limit of 360,000 characters), dropped z.md (over budget.max-chars) (run ctxtra doctor)\nInstructions:\n- a.md": {
			Files: files("a.md"), Dropped: append(over(files("b.md", "c.md", "d.md", "e.md"), "Claude Code's limit of 360,000 characters"),
				over(files("z.md"), "budget.max-chars")...)},
		"ctxtra added nothing; 2 problems (run ctxtra doctor)": {Problems: []string{"p", "q"}},
		"✓ ctxtra added 1 instruction\nInstructions:\n- a.md\n\nℹ team has new commits on main\nℹ n2": {
			Files: files("a.md"), Notices: []string{"team has new commits on main", "n2"}},
		// A notice after the skills list doesn't read as one of the skills.
		// The command on a notice's second line is indented under it.
		"✓ ctxtra added 2 skills\nSkills:\n- deploy\n- review\n\nℹ team has new commits on main\n  run `ctxtra source update team` to review them.": {
			Skills: skills, Notices: []string{"team has new commits on main\nrun `ctxtra source update team` to review them."}},
		"ctxtra added nothing\n\nℹ n": {Notices: []string{"n"}},
	} {
		if got := agent.Summary(res); got != want {
			t.Errorf("got  %q\nwant %q", got, want)
		}
	}
	if agent.Summary(nil) != "" || agent.Summary(&resolve.Result{}) != "" {
		t.Error("summary when nothing was added")
	}
}

func TestDelivery(t *testing.T) {
	d := agent.Delivery{Title: "Claude Code", Capacity: 360_000, Chunk: 9000, Slots: 40}
	if got := d.Over(); got != "Claude Code's limit of 360,000 characters" {
		t.Errorf("over: %q", got)
	}
	if got := (agent.Delivery{Title: "Codex", Capacity: 1_000_000, Unit: chunk.Bytes}).Over(); got != "Codex's limit of 1,000,000 bytes" {
		t.Errorf("over: %q", got)
	}
	// Three files of 140,000 characters: two fit 40 parts of 9,000, the third doesn't.
	res := &resolve.Result{Dir: "/x"}
	for _, n := range []string{"x", "y", "z"} {
		res.Files = append(res.Files, resolve.Hit{Path: "/" + n + ".md", Global: true,
			Content: strings.Repeat(strings.Repeat(n, 99)+"\n", 1400)})
	}
	d.Fit(res)
	if len(res.Files) != 2 || len(res.Dropped) != 1 || res.Dropped[0].Over != d.Over() {
		t.Fatalf("files %d, dropped %+v", len(res.Files), res.Dropped)
	}
	if n := len(d.Chunks(res)); n > 40 || n < 30 {
		t.Errorf("%d parts", n)
	}
	// Muse's parts count bytes of JSON: CJK text needs three times the
	// parts, and so do quotes and backslashes.
	muse := agent.Delivery{Title: "Muse", Capacity: 360_000, Unit: chunk.UTF16, Chunk: 13_000, ChunkJSON: true, Slots: 40}
	for _, line := range []string{"観測", `\"\"\"`} {
		res := &resolve.Result{Dir: "/x", Files: []resolve.Hit{{Path: "/c.md", Global: true, Content: strings.Repeat(line+"\n", 30_000)}}}
		parts := muse.Chunks(res)
		for _, p := range parts {
			if b, _ := jsonorder.Marshal(p); len(b)-2 > 13_000 {
				t.Errorf("%q: part of %d bytes of JSON", line, len(b)-2)
			}
		}
		if len(parts) < 17 {
			t.Errorf("%q: %d parts for over 210,000 bytes of JSON", line, len(parts))
		}
	}
	// Parts that don't fit the slots, below the capacity: a smaller chunk.
	small := agent.Delivery{Title: "Hermes", Capacity: 1_000_000, Chunk: 5000, Slots: 40}
	res.Files, res.Dropped = append(res.Files, res.Dropped...), nil
	small.Fit(res)
	if len(res.Files) != 1 {
		t.Errorf("files %d with 40 parts of 5,000", len(res.Files))
	}
}

// A setup with fewer slots gets fewer parts; one with more gets no more
// than the agent takes.
func TestWithSlots(t *testing.T) {
	d := agent.Delivery{Title: "Claude Code", Capacity: 360_000, Chunk: 9000, Slots: 40}
	if got := d.WithSlots(20); got.Slots != 20 || got.Capacity != 180_000 {
		t.Errorf("20 slots: %+v", got)
	}
	for _, n := range []int{0, 40, 64} {
		if got := d.WithSlots(n); got != d {
			t.Errorf("%d slots: %+v", n, got)
		}
	}
	muse := agent.Delivery{Title: "Muse", Capacity: 360_000, Unit: chunk.UTF16, Chunk: 13_000, ChunkJSON: true, Slots: 40}
	if got := muse.WithSlots(2); got.Slots != 2 || got.Capacity != 360_000 {
		t.Errorf("Muse, 2 slots: %+v", got)
	}
	if one := (agent.Delivery{Title: "Codex", Capacity: 1_000_000}); one.WithSlots(3) != one {
		t.Errorf("an agent without slots: %+v", one.WithSlots(3))
	}
}

func TestSystemMessage(t *testing.T) {
	if got := agent.SystemMessage("SessionStart", "a <b> & \"c\""); got != `{"systemMessage":"a <b> & \"c\""}` {
		t.Errorf("session start: %s", got)
	}
	if got := agent.SystemMessage("SubagentStart", "x"); got != "" {
		t.Errorf("subagent: %s", got)
	}
}

// Files with the same base name are shown by full path, and only those.
func TestSummarySameNames(t *testing.T) {
	h := testenv.Home(t)
	local := func(rel string) resolve.Hit { return resolve.Hit{Path: filepath.Join(h, rel)} }
	dropped := func(hit resolve.Hit) resolve.Hit { hit.Over = "budget.max-chars"; return hit }
	pinned := func(ref string) resolve.Hit {
		return resolve.Hit{Path: "/store/" + strings.TrimPrefix(ref, "ctxtra://"), Ref: ref}
	}
	for want, res := range map[string]*resolve.Result{
		"✓ ctxtra added 3 instructions\nInstructions:\n- other.md\n- ~/global/style.md\n- ~/personal/style.md": {
			Files: []resolve.Hit{local("global/other.md"), local("global/style.md"), local("personal/style.md")}},
		"✓ ctxtra added 2 instructions\nInstructions:\n- /etc/ctx/style.md\n- ~/global/style.md": {
			Files: []resolve.Hit{{Path: "/etc/ctx/style.md"}, local("global/style.md")}},
		"✓ ctxtra added 3 instructions\nInstructions:\n- engineering.md\n- ~/aictx/style.md\n- ctxtra://team-ctx/style.md": {
			Files: []resolve.Hit{local("aictx/engineering.md"), local("aictx/style.md"), pinned("ctxtra://team-ctx/style.md")}},
		"✓ ctxtra added 2 instructions\nInstructions:\n- ctxtra://a/style.md\n- ctxtra://b/style.md": {
			Files: []resolve.Hit{pinned("ctxtra://a/style.md"), pinned("ctxtra://b/style.md")}},
		"✓ ctxtra added 1 instruction\nInstructions:\n- style.md": {Files: []resolve.Hit{pinned("ctxtra://a/style.md")}},
		// A kept file and a dropped one are told apart too.
		"✓ ctxtra added 1 instruction; dropped ~/b/style.md, ctxtra://a/style.md, c.md (over budget.max-chars) (run ctxtra doctor)\nInstructions:\n- ~/a/style.md": {
			Files: []resolve.Hit{local("a/style.md")}, Dropped: []resolve.Hit{dropped(local("b/style.md")), dropped(pinned("ctxtra://a/style.md")), dropped(local("b/c.md"))}},
		"ctxtra added nothing; dropped ~/a/style.md, ~/b/style.md (over budget.max-chars) (run ctxtra doctor)": {
			Dropped: []resolve.Hit{dropped(local("a/style.md")), dropped(local("b/style.md"))}},
		// A line end in a parent folder stays on the line.
		`ctxtra added nothing; dropped "~/a\nb/style.md", ~/b/style.md (over budget.max-chars) (run ctxtra doctor)`: {
			Dropped: []resolve.Hit{dropped(local("a\nb/style.md")), dropped(local("b/style.md"))}},
		// Skills keep their names, which sessions use to tell them apart.
		"✓ ctxtra added 1 skill\nSkills:\n- deploy": {Skills: []resolve.Hit{local("skills/deploy/SKILL.md")}},
	} {
		for i := range res.Skills {
			res.Skills[i].Name = "deploy"
		}
		if got := agent.Summary(res); got != want {
			t.Errorf("got  %q\nwant %q", got, want)
		}
	}
}

// Resolved from a config: files of pinned sources by reference, never by
// their path in the store.
func TestSummaryPinnedFiles(t *testing.T) {
	h := testenv.Home(t)
	commit := strings.Repeat("a", 40)
	a, b := source.GenDir("https://h/a", commit), source.GenDir("https://h/b", commit)
	for _, f := range []string{filepath.Join(a, "style.md"), filepath.Join(a, "go", "style.md"), filepath.Join(b, "style.md"), filepath.Join(h, "ctx", "style.md")} {
		testenv.Write(t, f, "text")
	}
	cfg := testenv.Config(t, h, `schema: ctxtra/v1
sources:
  a: {git: 'https://h/a', branch: main, commit: '`+commit+`'}
  b: {git: 'https://h/b', branch: main, commit: '`+commit+`'}
bindings:
  - {name: all, match: {global: true}, instructions: [~/ctx/style.md, 'ctxtra://a/**/style.md', ctxtra://b/style.md]}
`)
	res, _ := resolve.Session(h, cfg)
	if len(res.Problems) > 0 {
		t.Fatalf("problems %v", res.Problems)
	}
	want := "✓ ctxtra added 4 instructions\nInstructions:\n- ~/ctx/style.md\n- ctxtra://a/go/style.md\n- ctxtra://a/style.md\n- ctxtra://b/style.md"
	if got := agent.Summary(res); got != want {
		t.Errorf("got  %q\nwant %q", got, want)
	}
}

func TestSummaryListLimit(t *testing.T) {
	for _, count := range []int{15, 16} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			res := &resolve.Result{}
			for i := 1; i <= count; i++ {
				name := fmt.Sprintf("item-%02d", i)
				res.Files = append(res.Files, resolve.Hit{Path: "/notes/" + name + ".md"})
				res.Skills = append(res.Skills, resolve.Hit{Name: name})
			}
			got := agent.Summary(res)
			if !strings.Contains(got, "\n- item-15.md") || !strings.Contains(got+"\n", "\n- item-15\n") {
				t.Fatalf("missing fifteenth entries: %q", got)
			}
			if strings.Contains(got, "item-16") || strings.Count(got, "\n- +1 more") != 2*(count-15) {
				t.Fatalf("incorrect overflow: %q", got)
			}
		})
	}
}
