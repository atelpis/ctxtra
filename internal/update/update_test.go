package update

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/atelpis/ctxtra/internal/config"
	"github.com/atelpis/ctxtra/internal/fsutil"
	"github.com/atelpis/ctxtra/internal/paths"
	"github.com/atelpis/ctxtra/internal/source"
	"github.com/atelpis/ctxtra/internal/state"
	"github.com/atelpis/ctxtra/internal/testenv"
)

func load(t *testing.T, path string) *config.Config {
	t.Helper()
	cfg := config.Load(path, fsutil.NewReader(config.MaxFileBytes, config.MaxSessionBytes))
	if len(cfg.Problems) > 0 {
		t.Fatalf("problems %v", cfg.Problems)
	}
	return cfg
}

func TestNewer(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want bool
	}{
		{"v0.3.0", "v0.2.3", true},
		{"v0.10.0", "v0.9.9", true},
		{"v1.0.0", "0.99.99", true},
		{"v0.2.3", "v0.2.3", false},
		{"v0.2.2", "v0.2.3", false},
		{"v0.3.0", "dev", false},
		{"v0.3.0", "v0.2.4-0.20261104010203-abcdefabcdef", false},
		{"v0.3.0-rc1", "v0.2.3", false},
		{"", "v0.2.3", false},
	} {
		if got := newer(c.a, c.b); got != c.want {
			t.Errorf("newer(%q, %q) = %v", c.a, c.b, got)
		}
	}
}

// release serves answer as GitHub's latest release, and counts requests.
func release(t *testing.T, status int, answer string) *int {
	t.Helper()
	n := new(int)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*n++
		if got := r.Header.Get("User-Agent"); !strings.HasPrefix(got, "ctxtra/") {
			t.Errorf("User-Agent %q", got)
		}
		w.WriteHeader(status)
		fmt.Fprint(w, answer)
	}))
	t.Cleanup(srv.Close)
	old := releaseURL
	releaseURL = srv.URL
	t.Cleanup(func() { releaseURL = old })
	return n
}

func TestLatestRelease(t *testing.T) {
	testenv.Home(t)
	for _, c := range []struct {
		status        int
		answer, want  string
		wantErrSubstr string
	}{
		{200, `{"tag_name": "v0.3.0", "body": "\u001b[31mignored"}`, "v0.3.0", ""},
		{200, `{"tag_name": "v0.3.0\u001b[31m"}`, "", "no version ctxtra can read"},
		{200, `not json`, "", "not a release"},
		{404, `{}`, "", "GitHub answered 404"},
	} {
		release(t, c.status, c.answer)
		got, err := latestRelease(context.Background(), "v0.2.3")
		if got != c.want || (c.wantErrSubstr == "") != (err == nil) || err != nil && !strings.Contains(err.Error(), c.wantErrSubstr) {
			t.Errorf("%d %s: %q, %v", c.status, c.answer, got, err)
		}
	}
}

func TestLatestReleaseTimeout(t *testing.T) {
	testenv.Home(t)
	block := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-block:
		case <-r.Context().Done():
		}
	}))
	defer srv.Close()
	defer close(block)
	oldURL, oldTimeout := releaseURL, releaseTimeout
	releaseURL, releaseTimeout = srv.URL, 50*time.Millisecond
	defer func() { releaseURL, releaseTimeout = oldURL, oldTimeout }()
	if _, err := latestRelease(context.Background(), "v0.2.3"); err == nil || !strings.Contains(err.Error(), "didn't answer in time") {
		t.Errorf("timeout: %v", err)
	}
}

// Run records what each check found, and why a check failed.
func TestRun(t *testing.T) {
	h := testenv.Home(t)
	dir := testenv.Repo(t, filepath.Join(h, "srv", "team"), "")
	url := testenv.GitServer(t, filepath.Join(h, "srv")) + "/team"
	headOf := func() string {
		t.Helper()
		out, err := exec.Command("git", "-C", dir, "rev-parse", "HEAD").Output()
		if err != nil {
			t.Fatal(err)
		}
		return strings.TrimSpace(string(out))
	}
	accepted := headOf()
	testenv.Write(t, filepath.Join(dir, "b.md"), "B")
	testenv.Run(t, dir, "git", "add", "-A")
	testenv.Run(t, dir, "git", "commit", "-q", "-m", "b")
	next := headOf()
	release(t, 200, `{"tag_name": "v0.3.0"}`)
	cfg := load(t, testenv.Config(t, h, `schema: ctxtra/v1
sources:
  team: {git: '`+url+`', branch: main, commit: '`+accepted+`', check-updates: true}
  gone: {git: 'https://127.0.0.1:1/gone', branch: main, check-updates: true}
  quiet: {git: 'https://127.0.0.1:1/quiet', branch: main}
  local: {local-git: '`+dir+`', check-updates: true}
`))
	results, err := Run(context.Background(), cfg, "v0.2.3", nil)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, r := range results {
		names = append(names, fmt.Sprintf("%v %s %v", r.Release, r.Name, r.Err != nil))
	}
	if got, want := strings.Join(names, "; "), "true  false; false team false; false gone true; false local false"; got != want {
		t.Errorf("results %q, want %q", got, want)
	}
	st := state.Load()
	if r := st.Ctxtra; r.Latest != "v0.3.0" || r.Checked.IsZero() || r.Attempt.IsZero() || r.Error != "" {
		t.Errorf("release state %+v", r)
	}
	if e := st.Get(source.ID(url), url, "main"); e.Head != next || e.Checked.IsZero() || e.Error != "" || !e.LastCheck.IsZero() {
		t.Errorf("team state %+v", e)
	}
	if e := st.Get(source.ID(dir), dir, ""); e.Head != next || e.Error != "" {
		t.Errorf("local state %+v", e)
	}
	gone := st.Get(source.ID("https://127.0.0.1:1/gone"), "https://127.0.0.1:1/gone", "main")
	if gone.Error == "" || gone.Head != "" || !gone.Checked.IsZero() || gone.Attempt.IsZero() {
		t.Errorf("failed check state %+v", gone)
	}
	if e := st.Get(source.ID("https://127.0.0.1:1/quiet"), "https://127.0.0.1:1/quiet", "main"); !e.Attempt.IsZero() {
		t.Errorf("a source without check-updates was checked: %+v", e)
	}

	// A failed check keeps what the last one that worked found.
	release(t, 500, ``)
	if _, err := Run(context.Background(), cfg, "v0.2.3", nil); err != nil {
		t.Fatal(err)
	}
	if r := state.Load().Ctxtra; r.Latest != "v0.3.0" || r.Error == "" || r.Checked.IsZero() {
		t.Errorf("after a failed check %+v", r)
	}

	// A background check checks only what its session claimed.
	requests := release(t, 200, `{"tag_name": "v0.3.0"}`)
	results, err = Run(context.Background(), cfg, "v0.2.3", &Selection{Sources: []string{"team", "quiet"}})
	if err != nil || len(results) != 1 || results[0].Name != "team" || *requests != 0 {
		t.Errorf("selection: %+v, %v, %d release requests", results, err, *requests)
	}
}

func TestStartDue(t *testing.T) {
	h := testenv.Home(t)
	t.Setenv(EnvOff, "")
	var started [][]string
	oldSpawn, oldNow := spawn, now
	spawn = func(args []string) error { started = append(started, args); return nil }
	at := time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)
	now = func() time.Time { return at }
	t.Cleanup(func() { spawn, now = oldSpawn, oldNow })
	cfgPath := testenv.Config(t, h, "schema: ctxtra/v1\n")
	want := func(name, version string, args ...string) {
		t.Helper()
		n := len(started)
		if err := StartDue(load(t, cfgPath), version); err != nil {
			t.Fatal(err)
		}
		switch {
		case len(args) == 0 && len(started) != n:
			t.Errorf("%s: started %q", name, started[n:])
		case len(args) > 0 && (len(started) != n+1 || strings.Join(started[n], " ") != strings.Join(args, " ")):
			t.Errorf("%s: started %q, want %q", name, started[n:], args)
		}
	}
	release := []string{"check-updates", "--background", "--release"}
	want("first session", "v0.2.3", release...)
	want("second session", "v0.2.3")
	at = at.Add(Interval - time.Minute)
	want("before the interval", "v0.2.3")
	at = at.Add(2 * time.Minute)
	want("after the interval", "v0.2.3", release...)

	at = at.Add(2 * Interval)
	t.Setenv(EnvOff, "1")
	want(EnvOff, "v0.2.3")
	t.Setenv(EnvOff, "")
	want("a dev build without source checks", "dev")
	cfgPath = testenv.Config(t, h, "schema: ctxtra/v1\ncheck-updates: false\n")
	want("check-updates: false", "v0.2.3")
	// A config that can't be used is no consent to a check: its
	// check-updates: false may be what failed.
	cfgPath = testenv.Config(t, h, "schema: ctxtra/v1\ncheck-updates: false\nbudget: {max-chars: -1}\n")
	if err := StartDue(config.Load(cfgPath, fsutil.NewReader(config.MaxFileBytes, config.MaxSessionBytes)), "v0.2.3"); err != nil || len(started) != 2 {
		t.Errorf("an ignored config: %q, %v", started, err)
	}

	// A source with check-updates on is due on its own, also in a dev build,
	// and the check checks only that source.
	cfgPath = testenv.Config(t, h, "schema: ctxtra/v1\nsources:\n  team: {git: 'https://h/team', branch: main, check-updates: true}\n")
	want("a source to check", "dev", "check-updates", "--background", "--source", "team")
	want("the source was just claimed", "dev")
	at = at.Add(Interval)
	want("both due", "v0.2.3", "check-updates", "--background", "--release", "--source", "team")

	// A check that can't start says so, for doctor.
	spawn = func([]string) error { return fmt.Errorf("no such file") }
	at = at.Add(Interval)
	if err := StartDue(load(t, cfgPath), "v0.2.3"); err == nil {
		t.Error("a failed start is no error")
	}
	st := state.Load()
	if e := st.Get(source.ID("https://h/team"), "https://h/team", "main"); e.Error != "can't start the check: no such file" || st.Ctxtra.Error != e.Error {
		t.Errorf("failed start: source %q, release %q", e.Error, st.Ctxtra.Error)
	}
	// A check that started after the claim keeps its result.
	spawn = func([]string) error {
		if err := state.UpdateRelease(func(r *state.Release) { r.Attempt, r.Error = at.Add(time.Second), "" }); err != nil {
			t.Fatal(err)
		}
		return fmt.Errorf("no such file")
	}
	at = at.Add(Interval)
	_ = StartDue(load(t, cfgPath), "v0.2.3")
	if st := state.Load(); st.Ctxtra.Error != "" {
		t.Errorf("a failed start replaced a newer result: %q", st.Ctxtra.Error)
	}

	// Saving the error doesn't make the session wait for a held lock, also
	// with many sources.
	many := "schema: ctxtra/v1\nsources:\n"
	for i := range 20 {
		many += fmt.Sprintf("  s%d: {git: 'https://h/s%d', branch: main, check-updates: true}\n", i, i)
	}
	cfgPath = testenv.Config(t, h, many)
	var unlock func()
	spawn = func([]string) error {
		var err error
		if unlock, err = fsutil.Lock(filepath.Join(paths.StateDir(), "state.lock")); err != nil {
			t.Fatal(err)
		}
		return fmt.Errorf("no such file")
	}
	at = at.Add(Interval)
	start := time.Now()
	_ = StartDue(load(t, cfgPath), "v0.2.3")
	if d := time.Since(start); d > time.Second {
		t.Errorf("StartDue waited %v for the lock", d)
	}
	unlock()
}

// The second of two sessions that start at once finds the first one's
// attempt, also when its own clock reading is a little earlier.
func TestClaimOrder(t *testing.T) {
	testenv.Home(t)
	at := time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)
	claim := func(t0 time.Time) bool {
		t.Helper()
		ok, _, _, err := state.ClaimChecks(true, nil, func() time.Time { return t0 }, Interval, time.Second)
		if err != nil {
			t.Fatal(err)
		}
		return ok
	}
	if !claim(at) {
		t.Fatal("first claim")
	}
	if claim(at.Add(-time.Second)) {
		t.Error("an earlier clock reading claimed again")
	}
	if !claim(at.Add(-2 * Interval)) {
		t.Error("after the clock went back by more than the interval, no check")
	}
}

func TestReleaseNotice(t *testing.T) {
	h := testenv.Home(t)
	on := load(t, testenv.Config(t, h, "schema: ctxtra/v1\n"))
	st := state.State{Ctxtra: state.Release{Latest: "v0.3.0"}}
	if got := ReleaseNotice(on, st, "v0.2.3"); got != "ctxtra update v0.3.0 is available - "+Releases {
		t.Errorf("newer: %q", got)
	}
	for _, v := range []string{"v0.3.0", "v0.4.0", "dev"} {
		if got := ReleaseNotice(on, st, v); got != "" {
			t.Errorf("%s: %q", v, got)
		}
	}
	// With a known install method, the command is on a second line.
	brew := filepath.Join(h, "Cellar", "ctxtra", "0.2.3", "bin", "ctxtra")
	testenv.Write(t, brew, "")
	old := executable
	executable = func() string { return brew }
	t.Cleanup(func() { executable = old })
	if got := ReleaseNotice(on, st, "v0.2.3"); got != "ctxtra update v0.3.0 is available - "+Releases+"\nrun `brew upgrade atelpis/tap/ctxtra` to update." {
		t.Errorf("Homebrew: %q", got)
	}
	off := load(t, testenv.Config(t, h, "schema: ctxtra/v1\ncheck-updates: false\n"))
	if got := ReleaseNotice(off, st, "v0.2.3"); got != "" {
		t.Errorf("check-updates: false: %q", got)
	}
}

func TestUpgradeCommand(t *testing.T) {
	h := testenv.Home(t)
	brew := filepath.Join(h, "brew", "Cellar", "ctxtra", "0.2.3", "bin", "ctxtra")
	testenv.Write(t, brew, "")
	link := filepath.Join(h, "brew", "bin", "ctxtra")
	os.MkdirAll(filepath.Dir(link), 0o755)
	if err := os.Symlink(brew, link); err != nil {
		t.Fatal(err)
	}
	gobin := filepath.Join(h, "gobin", "ctxtra")
	testenv.Write(t, gobin, "")
	other := filepath.Join(h, "opt", "ctxtra")
	testenv.Write(t, other, "")
	t.Setenv("GOBIN", filepath.Dir(gobin))
	for exe, want := range map[string]string{link: brewUpgrade, gobin: goInstall, other: "", filepath.Join(h, "none"): ""} {
		if got := upgradeCommand(exe); got != want {
			t.Errorf("%s: %q, want %q", exe, got, want)
		}
	}
	t.Setenv("GOBIN", "")
	t.Setenv("GOPATH", filepath.Join(h, "gp"))
	testenv.Write(t, filepath.Join(h, "gp", "bin", "ctxtra"), "")
	if got := upgradeCommand(filepath.Join(h, "gp", "bin", "ctxtra")); got != goInstall {
		t.Errorf("GOPATH: %q", got)
	}
}

// The result of a check that started before another one, and ended after
// it, doesn't replace the newer result, however long it took.
func TestRecordDropsSupersededResults(t *testing.T) {
	at := time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)
	clock := at.Add(2 * time.Minute)
	old := now
	now = func() time.Time { return clock }
	t.Cleanup(func() { now = old })
	var c state.Check
	if !Record(&c, at.Add(time.Minute), fmt.Errorf("GitHub answered 500")) || c.Error == "" {
		t.Fatalf("newer failure not recorded: %+v", c)
	}
	if Record(&c, at, nil) || c.Error == "" || !c.Checked.IsZero() || !c.Attempt.Equal(at.Add(time.Minute)) {
		t.Errorf("older success replaced the newer failure: %+v", c)
	}
	// A check that started before a laptop slept for 8 hours.
	if Record(&c, at.Add(-8*time.Hour), nil) || c.Error == "" {
		t.Errorf("a check from before sleep replaced the newer result: %+v", c)
	}
	// After the clock went back, the recorded attempt is in the future: it
	// doesn't count.
	clock = at.Add(-time.Hour)
	if !Record(&c, clock, nil) || c.Error != "" {
		t.Errorf("after the clock went back: %+v", c)
	}
}
