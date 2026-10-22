package agent_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/atelpis/ctxtra/internal/agent"
	"github.com/atelpis/ctxtra/internal/chunk"
	"github.com/atelpis/ctxtra/internal/jsonorder"
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
		"✓ ctxtra added 1 instruction; 1 not found, dropped b.md, c.md (over budget.max-chars), 1 problem (run ctxtra doctor)\nInstructions:\n- a.md": {
			Files: files("a.md"), Missing: []string{"x"}, Dropped: over(files("b.md", "c.md"), "budget.max-chars"), Problems: []string{"p"}},
		"✓ ctxtra added 1 instruction; dropped b.md, c.md, d.md, +1 more (over Claude Code's limit of 360,000 characters), dropped z.md (over budget.max-chars) (run ctxtra doctor)\nInstructions:\n- a.md": {
			Files: files("a.md"), Dropped: append(over(files("b.md", "c.md", "d.md", "e.md"), "Claude Code's limit of 360,000 characters"),
				over(files("z.md"), "budget.max-chars")...)},
		"ctxtra added nothing; 2 problems (run ctxtra doctor)": {Problems: []string{"p", "q"}},
		"✓ ctxtra added 1 instruction\nInstructions:\n- a.md\nctxtra: team last checked 9 days ago\nctxtra: n2": {
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
