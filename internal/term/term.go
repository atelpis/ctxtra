// Package term makes text safe to print to a terminal, and tells a terminal
// from other input.
package term

import (
	"fmt"
	"io"
	"strings"
	"unicode"
)

// Sanitize shows control characters other than newline and tab, and
// invisible formatting characters (such as right-to-left overrides), as
// escapes, so that text from a source can't move the cursor, hide lines or
// change colors.
func Sanitize(s string) string {
	if !strings.ContainsFunc(s, unsafe) {
		return s
	}
	var b strings.Builder
	for _, r := range s {
		if unsafe(r) {
			fmt.Fprintf(&b, `\u%04x`, r)
		} else {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func unsafe(r rune) bool {
	return r != '\n' && r != '\t' && (r == unicode.ReplacementChar || unicode.IsControl(r) || unicode.Is(unicode.Cf, r))
}

// Writer sanitizes everything written to W. Each Write must hold whole
// UTF-8 characters, as fmt's functions write.
type Writer struct{ W io.Writer }

func (w Writer) Write(p []byte) (int, error) {
	if _, err := io.WriteString(w.W, Sanitize(string(p))); err != nil {
		return 0, err
	}
	return len(p), nil
}
