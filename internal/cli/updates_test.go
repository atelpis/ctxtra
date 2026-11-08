package cli

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/ctxtra/ctxtra/internal/config"
	"github.com/ctxtra/ctxtra/internal/state"
	"github.com/ctxtra/ctxtra/internal/testenv"
)

// context files shows the release notice that sessions show, on one line.
func TestReportReleaseNotice(t *testing.T) {
	h := testenv.Home(t)
	testenv.Config(t, h, "schema: ctxtra/v1\n")
	if err := state.UpdateRelease(func(r *state.Release) { r.Latest = "v9.9.9" }); err != nil {
		t.Fatal(err)
	}
	var out, errOut bytes.Buffer
	r := report{IO{Out: &out, Err: &errOut}, "v0.2.3"}
	if code := r.files(h, false); code != 0 {
		t.Errorf("exit %d", code)
	}
	if got := errOut.String(); !strings.Contains(got, "notice: ctxtra update v9.9.9 is available - https://github.com/ctxtra/ctxtra/releases\n") {
		t.Errorf("stderr %q", got)
	}
}

// After a failed check, doctor says what the last one that worked found.
func TestStatusAfterFailure(t *testing.T) {
	at := time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)
	src := config.Source{Name: "team", Commit: strings.Repeat("a", 40), CheckUpdates: true}
	e := state.Source{Check: state.Check{Checked: at, Error: "git ls-remote: timed out"}, Head: strings.Repeat("b", 40)}
	want := "the last check for new commits failed: git ls-remote: timed out (the last one that worked, 2026-11-04 12:00, found new commits)"
	if got := checkStatus(src, e); got != want {
		t.Errorf("got  %q\nwant %q", got, want)
	}
	e.Checked = time.Time{}
	if got := checkStatus(src, e); got != "the last check for new commits failed: git ls-remote: timed out" {
		t.Errorf("never worked: %q", got)
	}
	cfg := &config.Config{CheckCtxtra: true}
	st := state.State{Ctxtra: state.Release{Check: state.Check{Checked: at, Error: "GitHub answered 500"}, Latest: "v0.3.0"}}
	if got := releaseStatus(cfg, st, "v0.2.3"); got != "the last check for a new release failed: GitHub answered 500 (the last one that worked, 2026-11-04 12:00, found latest release v0.3.0)" {
		t.Errorf("release: %q", got)
	}
}
