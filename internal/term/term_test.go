package term

import (
	"fmt"
	"io"
	"strings"
	"testing"
)

func TestSanitize(t *testing.T) {
	if got := Sanitize("a\x1b[31mb\u202ec\td\n\x1b]8;;x\x07é"); got != `a\u001b[31mb\u202ec`+"\td\n"+`\u001b]8;;x\u0007é` {
		t.Errorf("Sanitize: %q", got)
	}
	var b strings.Builder
	fmt.Fprintf(Writer{&b}, "x %s\n", "\x1b[2J")
	if b.String() != "x \\u001b[2J\n" {
		t.Errorf("Writer: %q", b.String())
	}
}

// Fprintf adds the style's escapes only for a terminal that shows colors,
// and sanitizes the text in both cases.
func TestFprintf(t *testing.T) {
	shows := colors
	t.Cleanup(func() { colors = shows })
	for _, tc := range []struct {
		colors bool
		want   string
	}{
		{false, `ok \u001b[2J` + "\n"},
		{true, "\x1b[32mok \\u001b[2J\x1b[0m\n"},
	} {
		colors = func(io.Writer) bool { return tc.colors }
		var b strings.Builder
		Fprintf(Writer{&b}, Green, "ok %s\n", "\x1b[2J")
		if b.String() != tc.want {
			t.Errorf("colors %v: %q, want %q", tc.colors, b.String(), tc.want)
		}
	}
	// Not a Writer: no colors.
	var b strings.Builder
	Fprintf(&b, Bold, "x")
	if b.String() != "x" {
		t.Errorf("not a Writer: %q", b.String())
	}
}
