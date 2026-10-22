package hook

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/atelpis/ctxtra/internal/agent/registry"
	"github.com/atelpis/ctxtra/internal/fsutil"
	"github.com/atelpis/ctxtra/internal/testenv"
)

// bigSession binds a file of about chars characters, of numbered lines.
func bigSession(t *testing.T, chars int) (home, dir string) {
	h := testenv.Home(t)
	r := testenv.Repo(t, filepath.Join(h, "svc"), "")
	var b strings.Builder
	for i := 0; b.Len() < chars; i++ {
		fmt.Fprintf(&b, "big L%05d some text that fills the line\n", i)
	}
	testenv.Write(t, filepath.Join(h, "big.md"), b.String())
	testenv.Write(t, filepath.Join(h, "z.md"), "LAST FILE")
	testenv.Write(t, filepath.Join(h, ".config", "ctxtra", "b.yaml"),
		"schema: ctxtra/v1\nbindings:\n  - {name: a, match: {global: true}, instructions: [~/big.md, ~/z.md]}\n")
	testenv.Config(t, h, "schema: ctxtra/v1\ninclude: [b.yaml]\n")
	orig := endLock
	endLock = func(unlock func()) { unlock() }
	t.Cleanup(func() { endLock = orig })
	return h, r
}

// slotAnswer is what one slot added.
type slotAnswer struct {
	text, summary string
	end           time.Time
}

// runSlots runs all 40 slots of claude's event at once, like Claude does.
func runSlots(t *testing.T, event, input string) []slotAnswer {
	t.Helper()
	return runSlotsOf(t, event, input, 40)
}

// runSlotsOf is runSlots for a setup with --of slots.
func runSlotsOf(t *testing.T, event, input string, slots int) []slotAnswer {
	t.Helper()
	out := make([]slotAnswer, slots)
	var wg sync.WaitGroup
	for k := range out {
		wg.Go(func() {
			var b bytes.Buffer
			RunArgs("claude", event, Args{Slot: k, Slots: slots}, strings.NewReader(input), &b, io.Discard)
			var m struct {
				HookSpecificOutput struct{ AdditionalContext string }
				SystemMessage      string
			}
			if err := json.Unmarshal(b.Bytes(), &m); err != nil {
				t.Errorf("slot %d: %q", k, b.String())
			}
			out[k] = slotAnswer{m.HookSpecificOutput.AdditionalContext, m.SystemMessage, time.Now()}
		})
	}
	wg.Wait()
	return out
}

func logEntries(t *testing.T, h string) []Entry {
	t.Helper()
	f, err := os.Open(filepath.Join(h, ".ctxtra", "log.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var out []Entry
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		var e Entry
		if json.Unmarshal(sc.Bytes(), &e) == nil {
			out = append(out, e)
		}
	}
	return out
}

// The slots of one session start resolve once, add the parts in order, and
// leave the snapshot readable by the user only.
func TestSlots(t *testing.T) {
	h, r := bigSession(t, 100_000)
	input := `{"cwd": "` + r + `", "session_id": "s1", "source": "startup"}`
	got := runSlots(t, "SessionStart", input)
	var parts []string
	for k, a := range got {
		if a.text != "" {
			parts = append(parts, a.text)
			if !strings.HasPrefix(a.text, fmt.Sprintf("[ctxtra part %d of ", k+1)) {
				t.Errorf("slot %d: %q", k, a.text[:40])
			}
			if k > 0 && a.end.Before(got[k-1].end) {
				t.Errorf("slot %d answered before slot %d", k, k-1)
			}
		}
		if (a.summary != "") != (k == 0) {
			t.Errorf("slot %d: summary %q", k, a.summary)
		}
	}
	if len(parts) < 12 || len(parts) > 13 {
		t.Errorf("%d parts", len(parts))
	}
	text := strings.Join(parts, "\n")
	for _, want := range []string{"big L00000 ", "big L02200 ", "LAST FILE"} {
		if strings.Count(text, want) != 1 {
			t.Errorf("%q %d times", want, strings.Count(text, want))
		}
	}
	if n := len(logEntries(t, h)); n != 1 {
		t.Errorf("%d log entries, want 1", n)
	}
	slots := filepath.Join(h, ".ctxtra", "slots")
	dirs, _ := os.ReadDir(slots)
	if len(dirs) != 1 {
		t.Fatalf("slot folders: %v", dirs)
	}
	if st, _ := os.Stat(filepath.Join(slots, dirs[0].Name())); st.Mode().Perm() != 0o700 {
		t.Errorf("folder mode %v", st.Mode())
	}
	gens := generations(filepath.Join(slots, dirs[0].Name()))
	if st, err := os.Stat(filepath.Join(slots, dirs[0].Name(), gens[0], "snapshot.json")); len(gens) != 1 || err != nil || st.Mode().Perm() != 0o600 {
		t.Errorf("generations %v, snapshot %v, %v", gens, st, err)
	}

	// A subagent of the same session is a session start of its own, and
	// gets no summary.
	got = runSlots(t, "SubagentStart", `{"cwd": "`+r+`", "session_id": "s1", "agent_id": "a1"}`)
	if !strings.Contains(got[0].text, "big L00000 ") || got[0].summary != "" {
		t.Errorf("subagent: %+v", got[0])
	}
	if n := len(logEntries(t, h)); n != 2 {
		t.Errorf("%d log entries, want 2", n)
	}
}

// age makes the snapshot of the newest generation in d written and read
// longer than joinWindow ago, and the folders older than keepSlots.
func age(t *testing.T, d string) {
	t.Helper()
	gens := generations(d)
	gen := filepath.Join(d, gens[len(gens)-1])
	b, err := os.ReadFile(filepath.Join(gen, "snapshot.json"))
	if err != nil {
		t.Fatal(err)
	}
	var s snapshot
	if err := json.Unmarshal(b, &s); err != nil {
		t.Fatal(err)
	}
	s.Made = s.Made.Add(-joinWindow - time.Second)
	b, _ = json.Marshal(s)
	testenv.Write(t, filepath.Join(gen, "snapshot.json"), string(b))
	read := time.Now().Add(-joinWindow - time.Second)
	os.Chtimes(filepath.Join(gen, "snapshot.json"), read, read)
	old := time.Now().Add(-2 * keepSlots)
	os.Chtimes(gen, old, old)
	os.Chtimes(d, old, old)
}

// The snapshot is readable by the user only and pinned for joinWindow: a
// slot joins it also when its number did, in the next round. Later, a slot
// starts a new generation, and folders of earlier session starts go.
func TestSnapshot(t *testing.T) {
	h, r := bigSession(t, 1000)
	p := payload{Cwd: r, SessionID: "s2", Source: "startup"}
	d := filepath.Join(h, ".ctxtra", "slots", slotKey("claude", "SessionStart", 40, p, r))
	calls := 0
	mk := func() snapshot { calls++; return snapshot{Parts: []string{fmt.Sprint(calls)}} }
	at, s, err := shared(d, 0, mk)
	if err != nil || s.Parts[0] != "1" || at.round != 0 || at.first {
		t.Fatalf("first: %+v, %+v, %v", at, s, err)
	}
	if st, err := os.Stat(filepath.Join(at.gen, "snapshot.json")); err != nil || st.Mode().Perm() != 0o600 {
		t.Errorf("snapshot: %v, %v", st, err)
	}
	for _, f := range []string{d, at.gen} {
		if st, err := os.Stat(f); err != nil || st.Mode().Perm() != 0o700 {
			t.Errorf("%s: %v, %v", f, st, err)
		}
	}
	for _, c := range []struct {
		slot, round int
		first       bool
	}{{1, 0, false}, {0, 1, true}, {1, 1, false}, {1, 2, true}, {0, 2, false}} {
		if a, s, _ := shared(d, c.slot, mk); s.Parts[0] != "1" || a.gen != at.gen || a.round != c.round || a.first != c.first {
			t.Errorf("slot %d: %+v, %+v, want round %d", c.slot, a, s, c.round)
		}
	}
	// After joinWindow: a new generation, resolved again.
	age(t, d)
	if a, s, _ := shared(d, 0, mk); s.Parts[0] != "2" || a.gen == at.gen || a.round != 0 {
		t.Errorf("after joinWindow: %+v, %+v", a, s)
	}
	// Folders of earlier session starts go, unless their lock is held; the
	// session start's own folder and its young generations stay.
	old := time.Now().Add(-time.Hour)
	stale, busy := filepath.Join(h, ".ctxtra", "slots", "stale"), filepath.Join(h, ".ctxtra", "slots", "busy")
	testenv.Write(t, filepath.Join(stale, "0000000000000001", "snapshot.json"), "{}")
	unlock, err := fsutil.LockTimeout(filepath.Join(busy, "lock"), 0)
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	oldGen := filepath.Join(d, fmt.Sprintf("%016x", old.UnixNano()))
	testenv.Write(t, filepath.Join(oldGen, "snapshot.json"), "{}")
	for _, f := range []string{stale, busy, oldGen} {
		ageAll(f, old)
	}
	age(t, d)
	shared(d, 2, mk)
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Errorf("stale folder left: %v", err)
	}
	if _, err := os.Stat(oldGen); !os.IsNotExist(err) {
		t.Errorf("old generation left: %v", err)
	}
	if _, err := os.Stat(busy); err != nil {
		t.Errorf("a folder whose lock is held: %v", err)
	}
	if n := len(generations(d)); n != 3 {
		t.Errorf("%d generations, want 3", n)
	}
	if slotKey("claude", "SessionStart", 0, payload{Cwd: r}, r) != "" {
		t.Error("a key without a session ID")
	}
}

// Cleaning never deletes the folder of the session start that cleans, nor
// its lock while it resolves (Codex's review).
func TestCleanCurrentFolder(t *testing.T) {
	d := filepath.Join(t.TempDir(), "slots", "current")
	if _, _, err := shared(d, 0, func() snapshot { return snapshot{Parts: []string{"old"}} }); err != nil {
		t.Fatal(err)
	}
	age(t, d)
	at, _, err := shared(d, 0, func() snapshot {
		if _, err := os.Stat(filepath.Join(d, "lock")); err != nil {
			t.Error("the lock was deleted while resolving:", err)
		}
		return snapshot{Parts: []string{"new"}}
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{d, at.gen} {
		if st, err := os.Stat(f); err != nil || st.Mode().Perm() != 0o700 {
			t.Errorf("%s: %v %v", f, st, err)
		}
	}
}

// While one slot resolves, another slot of an old folder waits, and then
// joins its generation (Codex's review).
func TestCleanKeepsLock(t *testing.T) {
	d := filepath.Join(t.TempDir(), "slots", "current")
	shared(d, 0, func() snapshot { return snapshot{Parts: []string{"old"}} })
	age(t, d)
	entered, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
	go func() {
		shared(d, 0, func() snapshot { close(entered); <-release; return snapshot{Parts: []string{"first"}} })
		close(done)
	}()
	<-entered
	overlap, second := make(chan struct{}), make(chan snapshot)
	go func() {
		_, s, _ := shared(d, 1, func() snapshot { close(overlap); return snapshot{Parts: []string{"second"}} })
		second <- s
	}()
	select {
	case <-overlap:
		t.Error("a second slot resolved while the first holds the lock")
	case <-time.After(time.Second):
	}
	close(release)
	<-done
	if s := <-second; s.Parts[0] != "first" {
		t.Errorf("second slot: %+v", s)
	}
}

// A session start that didn't finish, slot 0 of two parts only, and a
// second one within joinWindow whose slot 1 comes first: the second gets
// both parts of the first snapshot, never a mix (Codex's second review).
func TestSnapshotPinned(t *testing.T) {
	d := filepath.Join(t.TempDir(), "slots", "key")
	old := func() snapshot { return snapshot{Parts: []string{"OLD-0", "OLD-1"}} }
	fresh := func() snapshot { return snapshot{Parts: []string{"NEW-0", "NEW-1"}} }
	first, _, err := shared(d, 0, old)
	if err != nil {
		t.Fatal(err)
	}
	a1, s1, _ := shared(d, 1, fresh)
	a0, s0, _ := shared(d, 0, fresh)
	if a1.gen != first.gen || a0.gen != first.gen || s1.Parts[1] != "OLD-1" || s0.Parts[0] != "OLD-0" {
		t.Errorf("second start: slot 1 %+v %v, slot 0 %+v %v", a1, s1.Parts, a0, s0.Parts)
	}
	// Slot 1 is in round 0, after slot 0 of the first start, and slot 0 in
	// round 1, which it is the first of: it logs.
	if a1.round != 0 || a0.round != 1 || !a0.first {
		t.Errorf("rounds: slot 1 %+v, slot 0 %+v", a1, a0)
	}
}

// The pin counts from when a slot last read the snapshot: a session start
// that begins while the window ends still reads it with all its slots.
func TestSnapshotPinnedByReads(t *testing.T) {
	d := filepath.Join(t.TempDir(), "slots", "key")
	first, _, err := shared(d, 0, func() snapshot { return snapshot{Parts: []string{"OLD-0", "OLD-1"}} })
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(first.gen, "snapshot.json")
	almost := time.Now().Add(-joinWindow + 200*time.Millisecond)
	os.Chtimes(path, almost, almost)
	shared(d, 1, nil) // slot 1 of a second start, just within the window
	time.Sleep(300 * time.Millisecond)
	at, s, _ := shared(d, 0, func() snapshot { return snapshot{Parts: []string{"NEW-0", "NEW-1"}} })
	if at.gen != first.gen || s.Parts[0] != "OLD-0" {
		t.Errorf("slot 0 of the second start: %+v %v", at, s.Parts)
	}
}

// After joinWindow, a session start that didn't finish doesn't feed a
// later one: slot 0 ran alone, the file changed, and the next session
// start gets the new text (Codex's review). Within joinWindow it gets the
// pinned text, all of it, and logs that it did, once.
func TestSnapshotGeneration(t *testing.T) {
	h, r := bigSession(t, 1000)
	input := `{"cwd": "` + r + `", "session_id": "same", "source": "clear"}`
	p := payload{Cwd: r, SessionID: "same", Source: "clear"}
	d := filepath.Join(h, ".ctxtra", "slots", slotKey("claude", "SessionStart", 40, p, r))
	RunSlot("claude", "SessionStart", 0, strings.NewReader(input), io.Discard, io.Discard)
	testenv.Write(t, filepath.Join(h, "big.md"), "NEW-GENERATION-CONTEXT")
	if got := runSlots(t, "SessionStart", input); strings.Contains(got[0].text, "NEW-GENERATION-CONTEXT") || !strings.Contains(got[0].text, "big L00000") {
		t.Errorf("within joinWindow: %q", got[0].text)
	}
	e := logEntries(t, h)
	if len(e) != 2 || e[0].Reused != "" || !strings.HasPrefix(e[1].Reused, "the text of a session start ") || len(e[1].Files) != 2 {
		t.Errorf("log %+v", e)
	}
	age(t, d)
	if got := runSlots(t, "SessionStart", input); !strings.Contains(got[0].text, "NEW-GENERATION-CONTEXT") {
		t.Errorf("after joinWindow: %q", got[0].text)
	}
	if e := logEntries(t, h); len(e) != 3 || e[2].Reused != "" {
		t.Errorf("log %+v", e)
	}
}

// Two session starts of a text in several parts overlap: slots 0 to 9 of
// the first have joined and still run when the second starts, after the
// file changed. Every part of the second is from the first's snapshot, in
// order, each once; the second logs once, and resolves nothing.
func TestOverlappingGenerations(t *testing.T) {
	h, r := bigSession(t, 30_000)
	input := `{"cwd": "` + r + `", "session_id": "same", "source": "resume"}`
	p := payload{Cwd: r, SessionID: "same", Source: "resume"}
	d := filepath.Join(h, ".ctxtra", "slots", slotKey("claude", "SessionStart", 40, p, r))
	claude, _ := registry.Get("claude")
	var want []string
	for k := range 10 {
		_, s, err := shared(d, k, func() snapshot {
			res, e := session(claude, claude.Delivery(), "", "", "SessionStart", p, r)
			return snapshot{Parts: claude.Delivery().Chunks(res), Entry: &e}
		})
		if err != nil {
			t.Fatal(err)
		}
		want = s.Parts
	}
	if len(want) < 4 {
		t.Fatalf("%d parts", len(want))
	}
	testenv.Write(t, filepath.Join(h, "big.md"), "NEW-GENERATION-CONTEXT")
	got := runSlots(t, "SessionStart", input)
	for k, w := range want {
		if got[k].text != w {
			t.Errorf("part %d isn't the pinned one: %.80q", k+1, got[k].text)
		}
	}
	if got[len(want)].text != "" {
		t.Errorf("an extra part: %.80q", got[len(want)].text)
	}
	if e := logEntries(t, h); len(e) != 1 || e[0].Reused == "" {
		t.Errorf("log %+v", e)
	}
	if gens := generations(d); len(gens) != 1 {
		t.Errorf("generations %v, want 1", gens)
	}
}

// Without a session ID, each slot resolves on its own, and slot 0 logs.
func TestSlotsWithoutSession(t *testing.T) {
	h, r := bigSession(t, 30_000)
	got := runSlots(t, "SessionStart", `{"cwd": "`+r+`"}`)
	n := 0
	for _, a := range got {
		if a.text != "" {
			n++
		}
	}
	if n < 4 || !strings.Contains(got[n-1].text, "LAST FILE") {
		t.Errorf("%d parts", n)
	}
	if e := logEntries(t, h); len(e) != 1 {
		t.Errorf("%d log entries, want 1: %+v", len(e), e)
	}
	if _, err := os.Stat(filepath.Join(h, ".ctxtra", "slots")); !os.IsNotExist(err) {
		t.Errorf("slot folder: %v", err)
	}
}

// Over Claude's capacity, files are dropped from the end, the small one
// after the large one too, and the summary and the log say so.
func TestSlotsOverCapacity(t *testing.T) {
	h, r := bigSession(t, 370_000)
	got := runSlots(t, "SessionStart", `{"cwd": "`+r+`", "session_id": "s3"}`)
	if !strings.Contains(got[0].summary, "dropped big.md, z.md (over Claude Code's limit of 360,000 characters)") {
		t.Errorf("summary %q", got[0].summary)
	}
	if e := logEntries(t, h); len(e) != 1 || len(e[0].Dropped) != 2 || !strings.HasSuffix(e[0].Dropped[0], "big.md") {
		t.Errorf("log %+v", e)
	}
}

// When the state folder can't be written, as in an agent's sandbox, each
// slot resolves on its own: the text arrives in full, and slot 0 logs why.
func TestSlotsReadOnlyState(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root writes anyway")
	}
	h, r := bigSession(t, 30_000)
	logPath := filepath.Join(h, "log", "log.jsonl")
	t.Setenv("CTXTRA_LOG", logPath)
	state := filepath.Join(h, ".ctxtra")
	if err := os.MkdirAll(state, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(state, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(state, 0o755) })
	got := runSlots(t, "SessionStart", `{"cwd": "`+r+`", "session_id": "s4"}`)
	var parts []string
	for _, a := range got {
		if a.text != "" {
			parts = append(parts, a.text)
		}
	}
	text := strings.Join(parts, "\n")
	if len(parts) < 4 || !strings.Contains(text, "big L00000 ") || !strings.Contains(text, "LAST FILE") || got[0].summary == "" {
		t.Errorf("%d parts, summary %q", len(parts), got[0].summary)
	}
	b, err := os.ReadFile(logPath)
	if err != nil || strings.Count(string(b), "\n") != 1 || !strings.Contains(string(b), "the hooks can't share the session's text") {
		t.Errorf("log: %v\n%s", err, b)
	}
}

// ageAll sets the time of path and of everything in it: nothing used it
// since then.
func ageAll(path string, at time.Time) {
	filepath.WalkDir(path, func(p string, _ fs.DirEntry, err error) error {
		if err == nil {
			os.Chtimes(p, at, at)
		}
		return nil
	})
}

// A pin slides with its reads, so a generation can be older than keepSlots
// and still pinned: the cleanup of another session start keeps it, and the
// slots of the burst all get its text.
func TestPinSurvivesOtherCleanup(t *testing.T) {
	parent := filepath.Join(t.TempDir(), "slots")
	d := filepath.Join(parent, "a")
	text := func(parts ...string) func() snapshot { return func() snapshot { return snapshot{Parts: parts} } }
	at, _, err := shared(d, 0, text("OLD-0", "OLD-1", "OLD-2"))
	if err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-2 * time.Minute)
	aged := filepath.Join(d, fmt.Sprintf("%016x", old.UnixNano()))
	if err := os.Rename(at.gen, aged); err != nil {
		t.Fatal(err)
	}
	os.Chtimes(d, old, old)
	if _, s, err := shared(d, 1, text("UNEXPECTED")); err != nil || s.Parts[1] != "OLD-1" {
		t.Fatalf("slot 1 didn't join the pin: %+v %v", s, err)
	}
	if _, _, err := shared(filepath.Join(parent, "b"), 0, text("OTHER")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(aged, "snapshot.json")); err != nil {
		t.Errorf("another session start's cleanup deleted the pinned snapshot: %v", err)
	}
	if _, s, err := shared(d, 2, text("NEW-0", "NEW-1", "NEW-2")); err != nil || s.Parts[2] != "OLD-2" {
		t.Errorf("slot 2: %+v %v, want OLD-2 from the pin", s, err)
	}
}

// The slot count of a setup is part of the snapshot's key: setups with
// different counts of one session never share a snapshot, and each gets
// the text fitted to its count. A setup with more slots than this version
// has, after a downgrade, gets nothing in the slots past them.
func TestSlotCount(t *testing.T) {
	h, r := bigSession(t, 30_000)
	input := `{"cwd": "` + r + `", "session_id": "s6"}`
	two := runSlotsOf(t, "SessionStart", input, 2)
	if !strings.Contains(two[0].summary, "dropped big.md") || !strings.Contains(two[0].summary, "Claude Code's limit of 18,000 characters") ||
		strings.Contains(two[0].text+two[1].text, "big L00000") {
		t.Errorf("2 slots: %q, %.80q", two[0].summary, two[0].text)
	}
	forty := runSlotsOf(t, "SessionStart", input, 40)
	if !strings.Contains(forty[0].text, "big L00000") || strings.Contains(forty[0].summary, "dropped") {
		t.Errorf("40 slots: %q, %.80q", forty[0].summary, forty[0].text)
	}
	more := runSlotsOf(t, "SessionStart", input, 64)
	for k := range more {
		if k < 40 && more[k].text != forty[k].text || k >= 40 && more[k].text != "" {
			t.Errorf("64 slots: part %d is %.80q", k, more[k].text)
		}
	}
	if entries, _ := os.ReadDir(filepath.Join(h, ".ctxtra", "slots")); len(entries) != 3 {
		t.Errorf("want a folder per slot count, got %d", len(entries))
	}
	if e := logEntries(t, h); len(e) != 3 {
		t.Errorf("want a resolve per slot count, got %+v", e)
	}
}
