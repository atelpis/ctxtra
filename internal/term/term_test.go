package term

import (
	"fmt"
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
