package source

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/atelpis/ctxtra/internal/testenv"
)

// team creates a repository served by a test git server and returns its
// folder and URL.
func team(t *testing.T) (dir, url string) {
	t.Helper()
	h := testenv.Home(t)
	dir = testenv.Repo(t, filepath.Join(h, "srv", "team"), "")
	return dir, testenv.GitServer(t, filepath.Join(h, "srv")) + "/team"
}

func head(t *testing.T, dir string) string {
	t.Helper()
	out, err := exec.Command("git", "-C", dir, "rev-parse", "HEAD").Output()
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(out))
}

func commit(t *testing.T, dir string, files map[string]string) string {
	t.Helper()
	for p, c := range files {
		testenv.Write(t, filepath.Join(dir, p), c)
	}
	testenv.Run(t, dir, "git", "add", "-A")
	testenv.Run(t, dir, "git", "commit", "-q", "-m", "change")
	return head(t, dir)
}

func TestFetchAndMaterialize(t *testing.T) {
	dir, url := team(t)
	testenv.Write(t, filepath.Join(dir, "skills", "deploy", "run.sh"), "#!/bin/sh\n")
	os.Chmod(filepath.Join(dir, "skills", "deploy", "run.sh"), 0o755)
	os.Symlink("/etc/passwd", filepath.Join(dir, "link.md"))
	first := commit(t, dir, map[string]string{"a.md": "A\n"})
	ctx := context.Background()
	r := Open(url)

	if b, err := r.DefaultBranch(ctx); err != nil || b != "main" {
		t.Fatalf("default branch %q %v", b, err)
	}
	got, err := r.FetchBranch(ctx, "main")
	if err != nil || got != first {
		t.Fatalf("fetch: %s %v, want %s", got, err, first)
	}
	gen, err := r.Materialize(ctx, first)
	if err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(gen, "a.md")); string(b) != "A\n" {
		t.Errorf("a.md: %q", b)
	}
	if st, err := os.Stat(filepath.Join(gen, "skills", "deploy", "run.sh")); err != nil || st.Mode().Perm() != 0o500 {
		t.Errorf("run.sh: %v %v", st, err)
	}
	if Exists(filepath.Join(gen, "link.md")) {
		t.Error("a symlink entry was written")
	}
	if !Has(url, first) || gen != GenDir(url, first) {
		t.Errorf("Has %v, gen %s", Has(url, first), gen)
	}

	// A new commit, then a force-push that removes it: the accepted commit
	// stays available from ctxtra's copy, and fetching a removed commit by ID
	// fails.
	second := commit(t, dir, map[string]string{"a.md": "A2\n", "b.md": "\x1b[2Jhidden\n"})
	if got, _ := r.FetchBranch(ctx, "main"); got != second {
		t.Fatalf("second fetch %s", got)
	}
	if !r.IsAncestor(ctx, first, second) || r.IsAncestor(ctx, second, first) {
		t.Error("IsAncestor")
	}
	d, err := r.Diff(ctx, first, second)
	if err != nil || !strings.Contains(d, "+A2") || !strings.Contains(d, `\u001b[2Jhidden`) {
		t.Errorf("diff %v:\n%s", err, d)
	}
	testenv.Run(t, dir, "git", "reset", "-q", "--hard", first)
	third := commit(t, dir, map[string]string{"c.md": "C\n"})
	testenv.Run(t, dir, "git", "reflog", "expire", "--expire=now", "--all")
	testenv.Run(t, dir, "git", "gc", "-q", "--prune=now")
	if got, _ := r.FetchBranch(ctx, "main"); got != third || r.IsAncestor(ctx, second, third) {
		t.Errorf("after force-push: %s", got)
	}

	// A fresh store, as on a new laptop: restore fetches exact commits.
	os.RemoveAll(filepath.Join(os.Getenv("HOME"), ".ctxtra"))
	r = Open(url)
	if err := r.FetchCommit(ctx, first); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Materialize(ctx, first); err != nil {
		t.Fatal(err)
	}
	if err := r.FetchCommit(ctx, second); err == nil || !strings.Contains(err.Error(), "force-push") {
		t.Errorf("removed commit: %v", err)
	}
}

func TestTreeRefusesBadCommits(t *testing.T) {
	dir, url := team(t)
	ctx := context.Background()
	r := Open(url)
	big := commit(t, dir, map[string]string{"big.md": strings.Repeat("x", MaxFileBytes+1)})
	if _, err := r.FetchBranch(ctx, "main"); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Materialize(ctx, big); err == nil || !strings.Contains(err.Error(), "larger than") {
		t.Errorf("big file: %v", err)
	}
	if Has(url, big) {
		t.Error("a refused commit is in the store")
	}
	entries, _ := os.ReadDir(filepath.Dir(GenDir(url, big)))
	if len(entries) != 0 {
		t.Errorf("leftovers in the store: %v", entries)
	}
}

func TestChecks(t *testing.T) {
	for _, u := range []string{"https://github.com/a/b.git", "ssh://git@host:22/a/b", "git@github.com:a/b.git", "host:a/b"} {
		if err := CheckURL(u); err != nil {
			t.Errorf("%s: %v", u, err)
		}
	}
	for _, u := range []string{"", "-oProxyCommand=x", "file:///tmp/x", "/tmp/x", "./x", "ext::sh -c x", "http://h/a", "git://h/a", "https://h/a b", "c:/x", "x@-oProxyCommand=y:a", "ssh://-oProxyCommand=y/a", "https://-x/a", ".:x"} {
		if CheckURL(u) == nil {
			t.Errorf("%s: accepted", u)
		}
	}
	for _, b := range []string{"main", "release/1.2", "feature_x"} {
		if err := CheckBranch(b); err != nil {
			t.Errorf("%s: %v", b, err)
		}
	}
	for _, b := range []string{"", "-x", "a..b", "a/", "a.lock", "a/.b", "a b", "a//b", "HEAD^"} {
		if CheckBranch(b) == nil {
			t.Errorf("%s: accepted", b)
		}
	}
	for _, p := range []string{"", "/a", "a//b", "a/../b", ".git/config", "x/.GIT/y", "a\\b", "a\nb", "./a"} {
		if CheckPath(p) == nil {
			t.Errorf("%q: accepted", p)
		}
	}
	if !ValidCommit(strings.Repeat("a", 40)) || !ValidCommit(strings.Repeat("0", 64)) || ValidCommit("abc1234") || ValidCommit(strings.Repeat("A", 40)) {
		t.Error("ValidCommit")
	}
}

func TestNames(t *testing.T) {
	cases := map[string][]string{
		"folder case":     {"A/x.md", "a/y.md"},
		"file case":       {"README.md", "readme.md"},
		"file and folder": {"a", "A/b"},
		"unicode":         {"café.md", "café.md"},
		"german sharp s":  {"straße.md", "STRASSE.md"},
	}
	for name, ps := range cases {
		ns := names{}
		var err error
		for _, p := range ps {
			if err = ns.add(p); err != nil {
				break
			}
		}
		if err == nil {
			t.Errorf("%s: no collision found", name)
		}
	}
	ns := names{}
	for _, p := range []string{"a/x.md", "a/y.md", "a/b/z.md", "b.md"} {
		if err := ns.add(p); err != nil {
			t.Errorf("%s: %v", p, err)
		}
	}
}

func TestRepoSizeLimit(t *testing.T) {
	dir, url := team(t)
	// 3 MB of random data in the history, gone at the branch's head.
	testenv.Run(t, dir, "sh", "-c", "head -c 3000000 /dev/urandom > big.bin")
	commit(t, dir, nil)
	testenv.Run(t, dir, "git", "rm", "-q", "big.bin")
	commit(t, dir, nil)
	old := maxRepoBytes
	maxRepoBytes = 1 << 20
	t.Cleanup(func() { maxRepoBytes = old })
	if _, err := Open(url).FetchBranch(context.Background(), "main"); !errors.Is(err, errRepoSize) {
		t.Errorf("fetch of a large history: %v", err)
	}
}

// Two fetches of different branches at the same time each get their own
// branch's commit.
func TestConcurrentFetches(t *testing.T) {
	dir, url := team(t)
	main := head(t, dir)
	testenv.Run(t, dir, "git", "checkout", "-q", "-b", "other")
	other := commit(t, dir, map[string]string{"o.md": "o"})
	ctx := context.Background()
	r := Open(url)
	if _, err := r.FetchBranch(ctx, "main"); err != nil { // creates the repository
		t.Fatal(err)
	}
	type result struct{ got, want string }
	results := make(chan result, 20)
	for i := range 20 {
		go func() {
			branch, want := "main", main
			if i%2 == 1 {
				branch, want = "other", other
			}
			got, _ := Open(url).FetchBranch(ctx, branch)
			results <- result{got, want}
		}()
	}
	for range 20 {
		if r := <-results; r.got != r.want {
			t.Errorf("got %s, want %s", r.got, r.want)
		}
	}
}

func TestSHA256(t *testing.T) {
	h := testenv.Home(t)
	dir := filepath.Join(h, "srv", "team")
	testenv.Write(t, filepath.Join(dir, "a.md"), "A\n")
	testenv.Run(t, dir, "git", "init", "-q", "-b", "main", "--object-format=sha256")
	testenv.Run(t, dir, "git", "add", ".")
	testenv.Run(t, dir, "git", "commit", "-q", "-m", "init")
	url := testenv.GitServer(t, filepath.Join(h, "srv")) + "/team"
	ctx := context.Background()
	r := Open(url)
	c, err := r.FetchBranch(ctx, "main")
	if err != nil || len(c) != 64 || !ValidCommit(c) {
		t.Fatalf("%q %v", c, err)
	}
	gen, err := r.Materialize(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(gen, "a.md")); string(b) != "A\n" {
		t.Errorf("a.md %q", b)
	}
}

// The caps hold for real subprocess output: exec copies with ReadFrom when
// the writer has one.
func TestOutputCaps(t *testing.T) {
	testenv.Home(t)
	r := Open("https://h/x")
	if out, err := r.gitMax(context.Background(), false, nil, 1, "version"); err == nil || !strings.Contains(err.Error(), "more than 1 bytes") {
		t.Errorf("stdout over the cap: %q %v", out, err)
	}
	c := &capped{max: 10, drop: true}
	cmd := exec.Command("sh", "-c", "head -c 100000 /dev/zero >&2")
	cmd.Stderr = c
	if err := cmd.Run(); err != nil || c.buf.Len() != 10 || !c.over {
		t.Errorf("stderr: %v, %d bytes", err, c.buf.Len())
	}
}

// A commit within the limits is materialized, also when its files are more
// than one git command may print otherwise.
func TestLargeCommit(t *testing.T) {
	dir, url := team(t)
	c := commit(t, dir, map[string]string{"a.md": strings.Repeat("a", 9<<20), "b.md": strings.Repeat("b", 9<<20)})
	r := Open(url)
	ctx := context.Background()
	if _, err := r.FetchBranch(ctx, "main"); err != nil {
		t.Fatal(err)
	}
	gen, err := r.Materialize(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	if st, err := os.Stat(filepath.Join(gen, "b.md")); err != nil || st.Size() != 9<<20 {
		t.Errorf("b.md: %v %v", st, err)
	}
}

// A local-git source takes the clone's committed HEAD, detached too,
// never the working tree.
func TestFetchHead(t *testing.T) {
	h := testenv.Home(t)
	clone := testenv.Repo(t, filepath.Join(h, "clone"), "https://github.com/acme/team")
	first := head(t, clone)
	testenv.Write(t, filepath.Join(clone, "AGENTS.md"), "uncommitted\n")
	ctx := context.Background()
	r := Open(clone)
	got, err := r.FetchHead(ctx, clone)
	if err != nil || got != first {
		t.Fatalf("%s %v, want %s", got, err, first)
	}
	gen, err := r.Materialize(ctx, got)
	if err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(gen, "AGENTS.md")); string(b) != "team\n" {
		t.Errorf("AGENTS.md %q", b)
	}
	second := commit(t, clone, map[string]string{"b.md": "b"})
	testenv.Run(t, clone, "git", "checkout", "-q", "--detach", second)
	if got, err := r.FetchHead(ctx, clone); err != nil || got != second {
		t.Errorf("detached: %s %v", got, err)
	}
	// Restore from the clone by commit ID.
	os.RemoveAll(filepath.Join(h, ".ctxtra"))
	if err := Open(clone).FetchCommit(ctx, first); err != nil {
		t.Errorf("restore from the clone: %v", err)
	}
	// The file transport stays off for other sources.
	if _, err := Open("https://h/x").gitMax(ctx, false, nil, maxOutput, "ls-remote", "--", "file://"+clone); err == nil {
		t.Error("file transport allowed for a git source")
	}
}

func TestInspectClone(t *testing.T) {
	dir, url := team(t)
	h := os.Getenv("HOME")
	clone := filepath.Join(h, "dev", "team")
	testenv.Run(t, h, "git", "clone", "-q", url, clone)
	os.MkdirAll(filepath.Join(clone, "sub"), 0o755)
	c, err := InspectClone(context.Background(), filepath.Join(clone, "sub"))
	if err != nil {
		t.Fatal(err)
	}
	if c.Top != clone || c.Head != head(t, dir) || c.Remotes["origin"] != url || c.Upstream != "origin" || c.Branch != "main" {
		t.Errorf("%+v", c)
	}
	testenv.Run(t, clone, "git", "remote", "add", "fork", "git@github.com:me/team.git")
	testenv.Run(t, clone, "git", "checkout", "-q", "-b", "local")
	c, _ = InspectClone(context.Background(), clone)
	if len(c.Remotes) != 2 || c.Upstream != "" {
		t.Errorf("no upstream, two remotes: %+v", c)
	}
	if _, err := InspectClone(context.Background(), h); err == nil {
		t.Error("a folder outside git")
	}
}

func TestClean(t *testing.T) {
	testenv.Home(t)
	old := time.Now().Add(-40 * 24 * time.Hour)
	mk := func(url, commit string, mtime time.Time) string {
		p := GenDir(url, commit)
		testenv.Write(t, filepath.Join(p, "a.md"), "a")
		os.Chmod(filepath.Join(p, "a.md"), 0o400)
		os.Chtimes(p, mtime, mtime)
		return p
	}
	a, b, c := strings.Repeat("a", 40), strings.Repeat("b", 40), strings.Repeat("c", 40)
	kept := mk("https://h/x", a, old)
	gone := mk("https://h/x", b, old)
	recent := mk("https://h/x", c, time.Now())
	used := mk("https://h/y", a, old)
	Touch(used, 0)
	tmp := filepath.Join(filepath.Dir(kept), ".tmp-1")
	os.MkdirAll(tmp, 0o700)
	os.Chtimes(tmp, old, old)
	if n := Clean(map[string]bool{kept: true}, 30*24*time.Hour); n != 1 {
		t.Errorf("deleted %d", n)
	}
	for p, want := range map[string]bool{kept: true, gone: false, recent: true, used: true, tmp: false} {
		if Exists(p) != want {
			t.Errorf("%s exists: %v", p, !want)
		}
	}
}

// A session waits for cleaning only a short time, and then doesn't use the
// generation.
func TestTouchWaitsBounded(t *testing.T) {
	testenv.Home(t)
	unlock, err := storeLock(false, 0)
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	err = Touch(GenDir("https://h/x", strings.Repeat("a", 40)), 100*time.Millisecond)
	unlock()
	if err == nil || time.Since(start) > time.Second {
		t.Errorf("Touch under Clean's lock: %v after %v", err, time.Since(start))
	}
	if err := Touch(GenDir("https://h/x", strings.Repeat("a", 40)), 0); err != nil {
		t.Errorf("Touch after: %v", err)
	}
}

// With NoPrompt, a remote that asks for credentials gets no prompt: the
// user's askpass program isn't run.
func TestNoPrompt(t *testing.T) {
	testenv.Home(t)
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("WWW-Authenticate", `Basic realm="git"`)
		w.WriteHeader(http.StatusUnauthorized)
	}))
	t.Cleanup(srv.Close)
	asked := filepath.Join(t.TempDir(), "asked")
	askpass := filepath.Join(t.TempDir(), "askpass")
	testenv.Write(t, askpass, "#!/bin/sh\ntouch '"+asked+"'\necho x\n")
	if err := os.Chmod(askpass, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_ASKPASS", askpass)
	t.Setenv("GIT_SSL_NO_VERIFY", "1")
	t.Cleanup(func() { NoPrompt = false })
	for _, noPrompt := range []bool{true, false} {
		NoPrompt = noPrompt
		os.Remove(asked)
		if _, err := Open(srv.URL + "/team").DefaultBranch(context.Background()); err == nil {
			t.Fatalf("NoPrompt %v: no error", noPrompt)
		}
		if _, err := os.Stat(asked); (err == nil) == noPrompt {
			t.Errorf("NoPrompt %v: askpass ran: %v", noPrompt, err == nil)
		}
	}
}
