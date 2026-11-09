package names

import (
	"math/rand/v2"
	"slices"
	"strings"
	"testing"
)

func shown(named []Name) map[Tool]string {
	out := map[Tool]string{}
	for _, n := range named {
		out[n.Tool] = n.Shown
	}
	return out
}

func h(t Tool) string { return hash(t) }

func TestNames(t *testing.T) {
	long20 := strings.Repeat("s", 20)
	for _, tc := range []struct {
		tool      Tool
		want      string
		shortened bool
	}{
		{Tool{"linear_work", "list_issues"}, "linear_work__list_issues", false},
		{Tool{"gh", "create-issue"}, "gh__create_issue_" + h(Tool{"gh", "create-issue"})[:6], true},
		{Tool{"gh", "a.b:c"}, "gh__a_b_c_" + h(Tool{"gh", "a.b:c"})[:6], true},
		{Tool{"gh", "ünï"}, "gh___n__" + h(Tool{"gh", "ünï"})[:6], true},
		// 38 characters exactly: kept.
		{Tool{"linear_work", strings.Repeat("t", 25)}, "linear_work__" + strings.Repeat("t", 25), false},
		// 39: cut so that with _ and the hash it is 38.
		{Tool{"linear_work", strings.Repeat("t", 26)}, "linear_work__" + strings.Repeat("t", 18) + "_" + h(Tool{"linear_work", strings.Repeat("t", 26)})[:6], true},
		{Tool{"lw", "create_issue_with_very_long_name_for_tests"}, "lw__create_issue_with_very_long_" + h(Tool{"lw", "create_issue_with_very_long_name_for_tests"})[:6], true},
		// Changed and short: the hash is added without a cut.
		{Tool{"a", "x-y"}, "a__x_y_" + h(Tool{"a", "x-y"})[:6], true},
		{Tool{long20, "tool"}, long20 + "__tool", false},
		{Tool{long20, strings.Repeat("t", 30)}, long20 + "__" + strings.Repeat("t", 9) + "_" + h(Tool{long20, strings.Repeat("t", 30)})[:6], true},
	} {
		named, dropped := Assign([]Tool{tc.tool})
		if len(named) != 1 || len(dropped) != 0 {
			t.Fatalf("%v: %v %v", tc.tool, named, dropped)
		}
		n := named[0]
		if n.Shown != tc.want || n.Shortened != tc.shortened || len(n.Shown) > MaxLen {
			t.Errorf("%v: %q (shortened %v), want %q (%v)", tc.tool, n.Shown, n.Shortened, tc.want, tc.shortened)
		}
		if !strings.HasPrefix(n.Shown, tc.tool.Server+"__") {
			t.Errorf("%v: the server prefix is cut: %q", tc.tool, n.Shown)
		}
	}
}

// The examples in docs/spec.md.
func TestDocumentedExamples(t *testing.T) {
	named, _ := Assign([]Tool{{"gh", "create-issue"}, {"linear_work", "list_issues"}})
	got := shown(named)
	if got[Tool{"gh", "create-issue"}] != "gh__create_issue_ef677c" || got[Tool{"linear_work", "list_issues"}] != "linear_work__list_issues" {
		t.Errorf("update docs/spec.md, MCP servers, Tool names: %v", got)
	}
}

func TestDescribe(t *testing.T) {
	named, _ := Assign([]Tool{{"linear_work", "create-issue"}, {"linear_work", "list"}})
	if got := Describe(named[0], "Create an issue."); got != "[linear_work: create-issue] Create an issue." {
		t.Errorf("shortened: %q", got)
	}
	if got := Describe(named[0], ""); got != "[linear_work: create-issue]" {
		t.Errorf("no description: %q", got)
	}
	if got := Describe(named[1], "List."); got != "List." {
		t.Errorf("unchanged: %q", got)
	}
}

// fakeHash makes collisions: the hash depends only on the first letter of
// the tool, and is the same for the first digits.
func fakeHash(t *testing.T, f func(Tool) string) {
	old := hash
	hash = f
	t.Cleanup(func() { hash = old })
}

func pad(s string) string { return (s + strings.Repeat("0", 64))[:64] }

// Two tools of one server that shorten to the same name get longer hashes,
// and names that don't collide keep their 6 digits.
func TestCollisions(t *testing.T) {
	fakeHash(t, func(t Tool) string {
		switch t.Name {
		case "a-x":
			return pad("aaaaaa1111222222")
		case "a.x":
			return pad("aaaaaa2222333333")
		}
		return pad("bbbbbb")
	})
	named, dropped := Assign([]Tool{{"s", "a-x"}, {"s", "a.x"}, {"s", "b-x"}})
	got := shown(named)
	if len(dropped) != 0 || got[Tool{"s", "a-x"}] != "s__a_x_aaaaaa1111" || got[Tool{"s", "a.x"}] != "s__a_x_aaaaaa2222" ||
		got[Tool{"s", "b-x"}] != "s__b_x_bbbbbb" {
		t.Errorf("%v %v", got, dropped)
	}
}

// Repeated collisions go to the last step, which keeps server__ whole for
// a 20-character server name.
func TestRepeatedCollisions(t *testing.T) {
	long := strings.Repeat("s", 20)
	fakeHash(t, func(t Tool) string {
		if t.Name == strings.Repeat("x", 30) {
			return pad("abcdefabcdef111111")
		}
		return pad("abcdefabcdef222222")
	})
	a, b := Tool{long, strings.Repeat("x", 30)}, Tool{long, strings.Repeat("x", 30) + "y"}
	named, dropped := Assign([]Tool{b, a})
	got := shown(named)
	if len(dropped) != 0 || got[a] != long+"__"+"_"+"abcdefabcdef111" || got[b] != long+"__"+"_"+"abcdefabcdef222" {
		t.Errorf("%v %v", got, dropped)
	}
	for _, s := range got {
		if len(s) != MaxLen {
			t.Errorf("%q: %d characters", s, len(s))
		}
	}
	// Equal in every digit: the first in sorted order keeps the name.
	fakeHash(t, func(Tool) string { return pad("same") })
	named, dropped = Assign([]Tool{{"s", "b-x"}, {"s", "b.x"}})
	if len(named) != 1 || named[0].Name != "b-x" || len(dropped) != 1 || dropped[0].Name != "b.x" {
		t.Errorf("%v %v", named, dropped)
	}
}

// A shortened name equal to an unshortened one: the unshortened keeps it.
func TestLiteralCollision(t *testing.T) {
	fakeHash(t, func(Tool) string { return pad("abcdef0123456789abcdef") })
	plain, odd := Tool{"s", "a_x_abcdef"}, Tool{"s", "a-x"}
	named, dropped := Assign([]Tool{odd, plain})
	got := shown(named)
	if len(dropped) != 0 || got[plain] != "s__a_x_abcdef" || got[odd] != "s__a_x_abcdef0123" {
		t.Errorf("%v %v", got, dropped)
	}
}

// Names depend only on the tools, not on the order they arrive in.
func TestOrderIndependence(t *testing.T) {
	fakeHash(t, func(t Tool) string { return pad(strings.Repeat(t.Name[:1], 6) + t.Server) })
	var tools []Tool
	for _, s := range []string{"a", "bb", "linear_work"} {
		for _, n := range []string{"x-1", "x.2", "y-1", "x_1", "z", strings.Repeat("q", 40)} {
			tools = append(tools, Tool{s, n})
		}
	}
	first, firstDropped := Assign(tools)
	for range 50 {
		rand.Shuffle(len(tools), func(i, j int) { tools[i], tools[j] = tools[j], tools[i] })
		named, dropped := Assign(tools)
		if !slices.Equal(named, first) || !slices.Equal(dropped, firstDropped) {
			t.Fatalf("order changed the names")
		}
	}
	seen := map[string]bool{}
	for _, n := range first {
		if seen[n.Shown] {
			t.Errorf("duplicate %q", n.Shown)
		}
		seen[n.Shown] = true
	}
}

// Same tool, other session: the same name.
func TestStable(t *testing.T) {
	a, _ := Assign([]Tool{{"gh", "create-issue"}})
	b, _ := Assign([]Tool{{"gh", "create-issue"}, {"other", "x"}, {"gh", "list"}})
	if shown(a)[Tool{"gh", "create-issue"}] != shown(b)[Tool{"gh", "create-issue"}] {
		t.Errorf("%v %v", a, b)
	}
}
