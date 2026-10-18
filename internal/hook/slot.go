package hook

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/atelpis/ctxtra/internal/agent"
	"github.com/atelpis/ctxtra/internal/fsutil"
	"github.com/atelpis/ctxtra/internal/paths"
)

// Claude Code and Muse run all hooks of an event at once, and ctxtra has a
// hook per part of the text there. The first slot to get the lock resolves
// the session and writes a snapshot that the other slots read, so a session
// start is resolved and logged once. Claude orders the hooks' texts by when
// the hooks end: each slot answers after the slot before it ended, or after
// orderWait; the labels of the parts keep the text correct in any order.
//
// The waits add up to at most 100 ms for the slot's own lock, snapshotWait
// for the snapshot, and orderWait, counted from when the slot has it: 8.1 s,
// under the 10 s hook timeout. The slot that resolves waits for the
// resolving instead of snapshotWait.
const (
	snapshotWait = 6 * time.Second  // the most a slot waits for the snapshot
	orderWait    = 2 * time.Second  // the most a slot waits for the slot before it
	snapshotAge  = 20 * time.Second // an older snapshot is from an earlier session start
	keepSlots    = time.Minute      // folders of earlier session starts are deleted after this
	maxSnapshot  = 16 << 20
)

// endLock releases the lock a slot holds while it runs. The lock is kept
// until the process ends, which is when the agent counts the hook as
// ended. A variable for tests, which run slots in one process.
var endLock = func(unlock func()) {}

// snapshot is what all slots of one session start answer.
type snapshot struct {
	Parts   []string `json:"parts"`
	Summary string   `json:"summary,omitempty"`
	Missing []string `json:"missing,omitempty"`
}

func runSlot(a agent.Agent, sl agent.Slotted, slot int, ts, event string, p payload, dir string, out, errOut io.Writer) {
	resolveNow := func() (snapshot, Entry) {
		res, e := session(a, ts, event, p, dir)
		if e.Skipped != "" {
			return snapshot{}, e
		}
		return snapshot{Parts: a.Delivery().Chunks(res), Summary: agent.Summary(res), Missing: res.Missing}, e
	}
	var snap snapshot
	var resolved *Entry // the log entry, when this slot resolved the session
	key := slotKey(a.Name(), event, p, dir)
	err := errors.New("no session ID")
	if key != "" {
		d := filepath.Join(paths.StateDir(), "slots", key)
		if unlock, err := fsutil.LockTimeout(filepath.Join(d, fmt.Sprintf("run.%d", slot)), 100*time.Millisecond); err == nil {
			defer endLock(unlock)
		}
		snap, err = shared(d, func() snapshot {
			s, e := resolveNow()
			resolved = &e
			return s
		})
		if err == nil {
			if resolved != nil {
				logEntry(*resolved)
			}
			if slot > 0 && slot < len(snap.Parts) {
				waitFor(d, slot-1, time.Now().Add(orderWait))
			}
			defer finish(d, slot, a.Delivery().Slots)
		}
	}
	if err != nil {
		// Nothing tells the slots of one session start apart, or the state
		// folder can't be used, for example in a sandbox: each slot resolves
		// on its own, and slot 0 logs.
		if resolved == nil {
			s, e := resolveNow()
			snap, resolved = s, &e
		}
		if slot == 0 {
			if key != "" {
				resolved.Problems = append(slices.Clip(resolved.Problems), "the hooks can't share the session's text, each resolved it: "+err.Error())
			}
			logEntry(*resolved)
		}
	}
	text, summary := "", ""
	if slot < len(snap.Parts) {
		text = snap.Parts[slot]
	}
	if slot == 0 {
		summary = snap.Summary
		for _, m := range snap.Missing {
			fmt.Fprintf(errOut, "ctxtra: bound file not found: %s\n", m)
		}
	}
	write(out, sl.SlotAnswer(event, text, summary))
}

// slotKey names the session start of a hook input, or is "" when the input
// has no session ID.
func slotKey(harness, event string, p payload, dir string) string {
	if p.SessionID == "" {
		return ""
	}
	h := sha256.New()
	for _, s := range []string{harness, event, p.Source, p.SessionID, p.AgentID, dir} {
		fmt.Fprintf(h, "%d:%s\n", len(s), s)
	}
	return hex.EncodeToString(h.Sum(nil))[:32]
}

// shared returns the snapshot in folder d, or else makes it with compute
// and writes it, readable by the user only: it is the text of the user's
// files.
func shared(d string, compute func() snapshot) (snapshot, error) {
	unlock, err := fsutil.LockTimeout(filepath.Join(d, "lock"), snapshotWait)
	if err != nil {
		return snapshot{}, err
	}
	defer unlock()
	path := filepath.Join(d, "snapshot.json")
	if st, err := os.Lstat(path); err == nil && time.Since(st.ModTime()) < snapshotAge {
		var s snapshot
		b, err := fsutil.ReadBounded(path, maxSnapshot)
		if err == nil && json.Unmarshal(b, &s) == nil {
			return s, nil
		}
	}
	clean(filepath.Dir(d))
	// Marks of an earlier session start with the same key.
	if old, err := filepath.Glob(filepath.Join(d, "done.*")); err == nil {
		for _, f := range old {
			os.Remove(f)
		}
	}
	s := compute()
	b, err := json.Marshal(s)
	if err != nil {
		return snapshot{}, err
	}
	// The other slots can't read it then, and add nothing.
	if err := fsutil.WriteFile(path, b, 0o600); err != nil {
		return s, err
	}
	return s, nil
}

// clean deletes the folders of earlier session starts in dir.
func clean(dir string) {
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if st, err := e.Info(); err == nil && st.IsDir() && time.Since(st.ModTime()) > keepSlots {
			os.RemoveAll(filepath.Join(dir, e.Name()))
		}
	}
}

// waitFor waits until slot k in folder d has answered and ended, or until
// deadline.
func waitFor(d string, k int, deadline time.Time) {
	for {
		if _, err := os.Lstat(filepath.Join(d, fmt.Sprintf("done.%d", k))); err == nil {
			break
		}
		if time.Now().After(deadline) {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	// Slot k holds run.k until it ends.
	if unlock, err := fsutil.LockTimeout(filepath.Join(d, fmt.Sprintf("run.%d", k)), time.Until(deadline)); err == nil {
		unlock()
	}
}

// finish marks slot as done. The last of slots to finish deletes the
// snapshot: every slot has read it.
func finish(d string, slot, slots int) {
	if f, err := os.OpenFile(filepath.Join(d, fmt.Sprintf("done.%d", slot)), os.O_CREATE|os.O_WRONLY, 0o600); err == nil {
		f.Close()
	}
	for k := range slots {
		if _, err := os.Lstat(filepath.Join(d, fmt.Sprintf("done.%d", k))); err != nil {
			return
		}
	}
	os.Remove(filepath.Join(d, "snapshot.json"))
}
