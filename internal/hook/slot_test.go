package hook

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

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
	out := make([]slotAnswer, 40)
	var wg sync.WaitGroup
	for k := range out {
		wg.Go(func() {
			var b bytes.Buffer
			RunSlot("claude", event, k, strings.NewReader(input), &b, io.Discard)
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
// leave no copy of the text behind.
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
	if _, err := os.Stat(filepath.Join(slots, dirs[0].Name(), "snapshot.json")); !os.IsNotExist(err) {
		t.Errorf("snapshot left: %v", err)
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

// The snapshot is readable by the user only, a snapshot of an earlier
// session start isn't used, and folders of earlier session starts go.
func TestSnapshot(t *testing.T) {
	h, r := bigSession(t, 1000)
	p := payload{Cwd: r, SessionID: "s2", Source: "startup"}
	d := filepath.Join(h, ".ctxtra", "slots", slotKey("claude", "SessionStart", p, r))
	calls := 0
	mk := func() snapshot { calls++; return snapshot{Parts: []string{fmt.Sprint(calls)}} }
	if s, err := shared(d, mk); err != nil || s.Parts[0] != "1" {
		t.Fatalf("first: %+v, %v", s, err)
	}
	if st, err := os.Stat(filepath.Join(d, "snapshot.json")); err != nil || st.Mode().Perm() != 0o600 {
		t.Errorf("snapshot: %v, %v", st, err)
	}
	if s, _ := shared(d, mk); s.Parts[0] != "1" {
		t.Errorf("second: %+v", s)
	}
	old := time.Now().Add(-time.Hour)
	os.Chtimes(filepath.Join(d, "snapshot.json"), old, old)
	if s, _ := shared(d, mk); s.Parts[0] != "2" {
		t.Errorf("after an hour: %+v", s)
	}
	stale := filepath.Join(h, ".ctxtra", "slots", "stale")
	testenv.Write(t, filepath.Join(stale, "snapshot.json"), "{}")
	os.Chtimes(stale, old, old)
	os.Chtimes(filepath.Join(d, "snapshot.json"), old, old)
	shared(d, mk)
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Errorf("stale folder left: %v", err)
	}
	if slotKey("claude", "SessionStart", payload{Cwd: r}, r) != "" {
		t.Error("a key without a session ID")
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
	if n := len(logEntries(t, h)); n != 1 {
		t.Errorf("%d log entries, want 1", n)
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
