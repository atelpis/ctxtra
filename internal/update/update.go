// Package update checks for new ctxtra releases and for new commits of the
// pinned sources that have check-updates on.
//
// Sessions never wait on the network: the hook shows the results that
// state.json has from earlier checks, and starts a check in a detached
// process when one is due (StartDue). The check writes its results for the
// next session. A check reads only a version number or a commit ID: it
// fetches no files and accepts nothing.
package update

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/ctxtra/ctxtra/internal/config"
	"github.com/ctxtra/ctxtra/internal/source"
	"github.com/ctxtra/ctxtra/internal/state"
	"github.com/ctxtra/ctxtra/internal/term"
)

const (
	// Interval is how long a check's result is used before the next session
	// starts another check.
	Interval = 4 * time.Hour
	// EnvOff set to anything but "" stops the checks that sessions start.
	EnvOff = "CTXTRA_NO_UPDATE_CHECK"
	// Releases is where users download ctxtra.
	Releases = "https://github.com/ctxtra/ctxtra/releases"

	brewUpgrade = "brew upgrade atelpis/tap/ctxtra"
	goInstall   = "go install github.com/ctxtra/ctxtra/cmd/ctxtra@latest"
)

// Variables for tests.
var (
	releaseURL     = "https://api.github.com/repos/ctxtra/ctxtra/releases/latest"
	releaseTimeout = 10 * time.Second
	sourceTimeout  = 30 * time.Second
	claimWait      = 200 * time.Millisecond
	// resultWait limits how long a check waits for the lock to save a result.
	resultWait = 10 * time.Second
	now        = time.Now
	// spawn starts ctxtra with args without waiting for it.
	spawn = start
)

// reRelease is a release version: what goreleaser and go install report
// for a tag. Pseudo-versions, dirty builds and "dev" don't match.
var reRelease = regexp.MustCompile(`^v?(\d+)\.(\d+)\.(\d+)$`)

// parse reads a release version.
func parse(v string) ([3]int, bool) {
	m := reRelease.FindStringSubmatch(v)
	if m == nil {
		return [3]int{}, false
	}
	var out [3]int
	for i := range out {
		n, err := strconv.Atoi(m[i+1])
		if err != nil {
			return [3]int{}, false
		}
		out[i] = n
	}
	return out, true
}

// IsRelease reports whether version is a release, which a check can compare
// with the latest one.
func IsRelease(version string) bool {
	_, ok := parse(version)
	return ok
}

// newer reports whether release version a is newer than b.
func newer(a, b string) bool {
	x, ok1 := parse(a)
	y, ok2 := parse(b)
	if !ok1 || !ok2 {
		return false
	}
	for i := range x {
		if x[i] != y[i] {
			return x[i] > y[i]
		}
	}
	return false
}

// Sources are the sources of cfg that have check-updates on. A config that
// is ignored as a whole has none.
func Sources(cfg *config.Config) []config.Source {
	if cfg.Ignored() {
		return nil
	}
	var out []config.Source
	for _, s := range cfg.Sources {
		if s.CheckUpdates && s.Remote() != "" {
			out = append(out, s)
		}
	}
	return out
}

// StartDue starts a check in the background when one is due: the release
// check is on and this is a release build, or a source has check-updates
// on, and its last attempt is older than Interval. The background check
// checks only what this call claimed. It returns at once.
func StartDue(cfg *config.Config, version string) error {
	if os.Getenv(EnvOff) != "" {
		return nil
	}
	release := cfg.CheckCtxtra && IsRelease(version)
	var claims []state.Claim
	for _, s := range Sources(cfg) {
		claims = append(claims, state.Claim{Name: s.Name, ID: source.ID(s.Remote()), URL: s.Remote(), Branch: s.Branch})
	}
	if !release && len(claims) == 0 {
		return nil
	}
	claimedRelease, claimed, claimedAt, err := state.ClaimChecks(release, claims, now, Interval, claimWait)
	if err != nil || !claimedRelease && len(claimed) == 0 {
		return err
	}
	args := []string{"check-updates", "--background"}
	if claimedRelease {
		args = append(args, "--release")
	}
	for _, c := range claimed {
		args = append(args, "--source", c.Name)
	}
	if err := spawn(args); err != nil {
		// Recorded, so that doctor says why no result comes before the next
		// attempt: only on claims that no later check replaced, and without
		// making the session wait for the lock.
		msg := term.Sanitize("can't start the check: " + err.Error())
		mine := func(c *state.Check) {
			if c.Attempt.Equal(claimedAt) {
				c.Error = msg
			}
		}
		_ = state.ChangeWithin(claimWait, func(s *state.State) {
			if claimedRelease {
				mine(&s.Ctxtra.Check)
			}
			for _, c := range claimed {
				e := s.Get(c.ID, c.URL, c.Branch)
				mine(&e.Check)
				s.Put(c.ID, e)
			}
		})
		return err
	}
	return nil
}

// Available is the newer ctxtra release that an earlier check found, and
// the command that upgrades this ctxtra to it ("" when the install method
// is unknown). latest is "" when there is none.
func Available(cfg *config.Config, st state.State, version string) (latest, command string) {
	latest = st.Ctxtra.Latest
	if !cfg.CheckCtxtra || !newer(latest, version) {
		return "", ""
	}
	return latest, upgradeCommand(executable())
}

// ReleaseNotice is the notice about a newer ctxtra release that an earlier
// check found, or "". The command to upgrade, when known, is on a second
// line.
func ReleaseNotice(cfg *config.Config, st state.State, version string) string {
	latest, command := Available(cfg, st, version)
	if latest == "" {
		return ""
	}
	n := fmt.Sprintf("ctxtra update %s is available - %s", latest, Releases)
	if command != "" {
		n += "\nrun `" + command + "` to update."
	}
	return n
}

// upgradeCommand is the command that upgrades the ctxtra at exe, when its
// install method is known: Homebrew or go install.
func upgradeCommand(exe string) string {
	real, err := filepath.EvalSymlinks(exe)
	if err != nil {
		return ""
	}
	if strings.Contains(real, string(filepath.Separator)+"Cellar"+string(filepath.Separator)+"ctxtra"+string(filepath.Separator)) {
		return brewUpgrade
	}
	gobin := os.Getenv("GOBIN")
	if gobin == "" {
		gopath := filepath.SplitList(os.Getenv("GOPATH"))
		if len(gopath) > 0 && gopath[0] != "" {
			gobin = filepath.Join(gopath[0], "bin")
		} else if home, err := os.UserHomeDir(); err == nil {
			gobin = filepath.Join(home, "go", "bin")
		}
	}
	if gobin != "" {
		if dir, err := filepath.EvalSymlinks(gobin); err == nil && dir == filepath.Dir(real) {
			return goInstall
		}
	}
	return ""
}

// executable is the path this ctxtra was started by, as the hook command
// names it (a symlink stays a symlink), else the running binary. A
// variable for tests.
var executable = func() string {
	if filepath.IsAbs(os.Args[0]) {
		return os.Args[0]
	}
	exe, _ := os.Executable()
	return exe
}

// Result is what one check found.
type Result struct {
	Release bool   // the release check, else a source's
	Name    string // the source's name
	Latest  string // the latest release, or the source's commit
	At      time.Time
	Err     error
	order   int
}

// Selection is what a check checks: the release, when the release check is
// on, and the named sources, when they have check-updates on.
type Selection struct {
	Release bool
	Sources []string
}

// Run checks what sel selects, or with nil everything that is on, in
// parallel, and records the results. It returns them in config order, the
// release first, and the first error writing them.
func Run(ctx context.Context, cfg *config.Config, version string, sel *Selection) ([]Result, error) {
	var (
		results []Result
		mu      sync.Mutex
		wg      sync.WaitGroup
		werr    error
	)
	add := func(r Result, err error) {
		mu.Lock()
		defer mu.Unlock()
		results = append(results, r)
		if err != nil && werr == nil {
			werr = err
		}
	}
	if cfg.CheckCtxtra && (sel == nil || sel.Release) {
		wg.Go(func() {
			at := now()
			latest, err := latestRelease(ctx, version)
			add(Result{Release: true, Latest: latest, At: at, Err: err, order: -1}, state.UpdateReleaseWithin(resultWait, func(r *state.Release) {
				if Record(&r.Check, at, err) && err == nil {
					r.Latest = latest
				}
			}))
		})
	}
	sem := make(chan struct{}, 4)
	for i, s := range Sources(cfg) {
		if sel != nil && !slices.Contains(sel.Sources, s.Name) {
			continue
		}
		wg.Go(func() {
			sem <- struct{}{}
			defer func() { <-sem }()
			at := now()
			var head string
			err := errNoTime
			// A check that waited past the deadline doesn't ask the remote:
			// its error says why, not that the remote failed.
			if ctx.Err() == nil {
				ctx, cancel := context.WithTimeout(ctx, sourceTimeout)
				defer cancel()
				head, err = source.Open(s.Remote()).Head(ctx, s.Branch)
			}
			add(Result{Name: s.Name, Latest: head, At: at, Err: err, order: i}, state.UpdateWithin(resultWait, source.ID(s.Remote()), s.Remote(), s.Branch, func(e *state.Source) {
				if Record(&e.Check, at, err) && err == nil {
					e.Head = head
				}
			}))
		})
	}
	wg.Wait()
	slices.SortFunc(results, func(a, b Result) int { return a.order - b.order })
	return results, werr
}

// Record sets the result of a check that started at at, and reports whether
// it did. A check that started later has the newer result, so the result
// of one that started earlier and ended after it is dropped, however long
// it took (a laptop can sleep during a check). An attempt later than now is
// from before the clock went back: it doesn't count.
func Record(c *state.Check, at time.Time, err error) bool {
	if c.Attempt.After(at) && !c.Attempt.After(now()) {
		return false
	}
	c.Attempt = at
	if err != nil {
		c.Error = term.Sanitize(err.Error())
		return true
	}
	c.Checked, c.Error = at, ""
	return true
}

// errNoTime is the result of a source check that a background check had no
// time left for.
var errNoTime = errors.New("not checked: the background check ran out of time")

// maxReleaseBytes limits the API's answer; a release is a few KB.
const maxReleaseBytes = 1 << 20

// latestRelease asks GitHub for the latest ctxtra release, prereleases and
// drafts left out, and returns its version. Only the version is read from
// the answer.
func latestRelease(ctx context.Context, version string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, releaseTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, releaseURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "ctxtra/"+version)
	client := &http.Client{CheckRedirect: func(r *http.Request, via []*http.Request) error {
		if r.URL.Scheme != "https" || len(via) >= 3 {
			return errors.New("unexpected redirect")
		}
		return nil
	}}
	resp, err := client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return "", errors.New("GitHub didn't answer in time")
		}
		return "", fmt.Errorf("can't reach GitHub: %w", errors.Unwrap(err))
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("GitHub answered %s", resp.Status)
	}
	var rel struct {
		Tag string `json:"tag_name"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxReleaseBytes)).Decode(&rel); err != nil {
		return "", errors.New("GitHub's answer is not a release")
	}
	if !IsRelease(rel.Tag) {
		return "", errors.New("GitHub's latest release has no version ctxtra can read")
	}
	return rel.Tag, nil
}
