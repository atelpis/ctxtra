package muse

import (
	"strings"
	"testing"

	"github.com/atelpis/ctxtra/internal/jsonorder"
)

// A long summary is cut, so the answer stays within what Muse takes.
func TestClip(t *testing.T) {
	if got := clip("ctxtra: added 1 file (a.md)"); got != "ctxtra: added 1 file (a.md)" {
		t.Errorf("short summary: %q", got)
	}
	long := strings.Repeat("ctxtra: \"source\" last checked 9 days ago; run ctxtra sync\n", 100)
	got := clip(long)
	if b, _ := jsonorder.Marshal(got); len(b)-2 > maxSummary || !strings.HasSuffix(got, "…") || !strings.HasPrefix(long, strings.TrimSuffix(got, "…")) {
		t.Errorf("%d bytes of JSON: %q", len(b)-2, got)
	}
}
