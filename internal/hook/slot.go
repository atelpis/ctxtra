package hook

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/atelpis/ctxtra/internal/agent"
	"github.com/atelpis/ctxtra/internal/fsutil"
	"github.com/atelpis/ctxtra/internal/paths"
)

// Claude Code and Muse run all hooks of an event at once, and ctxtra has a
// hook per part of the text there. The first slot to get the lock resolves
// the session and writes a snapshot that the other slots read, so a session
// start is resolved and logged once. Claude orders the hooks' texts by when
// the hooks end: each slot answers orderGap after the slot before it ended,
// or after orderWait; the labels of the parts keep the text correct in any
// order. Without the gap, Claude on Linux sometimes swaps two slots that end
// within a millisecond of each other.
//
// The waits add up to at most 100 ms for the slot's own lock, snapshotWait
// for the snapshot, and orderWait, counted from when the slot has it: 8.1 s,
// under the 10 s hook timeout. The slot that resolves waits for the
// resolving instead of snapshotWait.
//
// Nothing in the hook input tells two session starts with the same session
// ID, event and source apart, for example two /clear in a row, or a retry
// after a start that didn't finish. So a snapshot is pinned: every slot that
// comes within joinWindow after the snapshot was written, or after the last
// slot read it, reads it too, also when a slot with its number already did,
// and a slot that comes later starts a new generation, a folder of its own,
// and resolves. The slots of one session start begin within milliseconds of
// each other, and under load within a second or so; slots that waited
// snapshotWait for the lock join at once, because the age counts from when
// the snapshot was written. So the parts of two snapshots mix only when two
// slots of one session start begin more than joinWindow apart. The cost: a
// second session start with the same key within joinWindow gets the text as
// it was at the first one, and the log says so.
//
// Each slot that joins a generation gets a round, the number of slots with
// its number that joined before it, and orders itself after the slot before
// it in the same round, so a second session start never waits for the ended
// slots of the first one.
const (
	snapshotWait = 6 * time.Second // the most a slot waits for the snapshot
	orderWait    = 2 * time.Second // the most a slot waits for the slot before it
	orderGap     = 20 * time.Millisecond
	joinWindow   = 3 * time.Second // a snapshot is pinned this long after it was last read
	keepSlots    = time.Minute     // folders of earlier session starts are deleted after this
	maxSnapshot  = 16 << 20
)

// endLock releases the lock a slot holds while it runs. The lock is kept
// until the process ends, which is when the agent counts the hook as
// ended. A variable for tests, which run slots in one process.
var endLock = func(unlock func()) {}

// snapshot is what all slots of one session start answer, and its log
// entry, which a later session start that reads it logs again.
type snapshot struct {
	Parts   []string  `json:"parts"`
	Summary string    `json:"summary,omitempty"`
	Missing []string  `json:"missing,omitempty"`
	Made    time.Time `json:"made"`
	Entry   *Entry    `json:"entry,omitempty"`
}

// seat is where a slot joined: its generation, its round there, and whether
// it is the first slot of a round after the first, which logs.
type seat struct {
	gen   string
	round int
	first bool
}

func runSlot(a agent.Agent, sl agent.Slotted, slot int, ts, event string, p payload, in agent.Input, dir string, out, errOut io.Writer) {
	resolveNow := func() (snapshot, Entry) {
		res, e := session(a, ts, event, p, in, dir)
		return snapshot{Parts: a.Delivery().Chunks(res), Summary: agent.Summary(res), Missing: res.Missing}, e
	}
	var snap snapshot
	var resolved *Entry // the log entry, when this slot resolved the session
	key := slotKey(a.Name(), event, p, dir)
	err := errors.New("no session ID")
	if key != "" {
		var at seat
		at, snap, err = shared(filepath.Join(paths.StateDir(), "slots", key), slot, func() snapshot {
			s, e := resolveNow()
			resolved = &e
			s.Entry = &e
			return s
		})
		if err == nil {
			mark := func(what string, k int) string {
				return filepath.Join(at.gen, fmt.Sprintf("%s.%d.%d", what, k, at.round))
			}
			if unlock, err := fsutil.LockTimeout(mark("run", slot), 100*time.Millisecond); err == nil {
				defer endLock(unlock)
			}
			switch {
			case resolved != nil:
				logEntry(*resolved)
			case at.first && snap.Entry != nil:
				e := *snap.Entry
				e.Ts = ts
				e.Reused = fmt.Sprintf("the text of a session start %d ms earlier", time.Since(snap.Made).Milliseconds())
				logEntry(e)
			}
			if slot > 0 && slot < len(snap.Parts) {
				waitFor(mark("done", slot-1), mark("run", slot-1), time.Now().Add(orderWait))
			}
			defer finish(at, slot)
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

// shared returns where slot joins in folder d, and the snapshot there. When
// the newest generation's snapshot is older than joinWindow, shared makes a
// new generation with compute and writes the snapshot there, readable by
// the user only: it is the text of the user's files. Folders are readable by
// the user only too.
func shared(d string, slot int, compute func() snapshot) (seat, snapshot, error) {
	unlock, err := fsutil.LockTimeout(filepath.Join(d, "lock"), snapshotWait)
	if err != nil {
		return seat{}, snapshot{}, err
	}
	defer unlock()
	gens := generations(d)
	if len(gens) > 0 {
		gen := filepath.Join(d, gens[len(gens)-1])
		if at, s, ok := join(gen, slot); ok {
			return at, s, nil
		}
	}
	clean(d, gens)
	// A new name, later than every one before: generations sort by name.
	name := fmt.Sprintf("%016x", time.Now().UnixNano())
	if len(gens) > 0 && name <= gens[len(gens)-1] {
		return seat{}, snapshot{}, fmt.Errorf("%s: a generation from the future", d)
	}
	at := seat{gen: filepath.Join(d, name)}
	if err := os.Mkdir(at.gen, 0o700); err != nil {
		return seat{}, snapshot{}, err
	}
	s := compute()
	s.Made = time.Now()
	b, err := json.Marshal(s)
	if err != nil {
		return seat{}, s, err
	}
	// The other slots can't read it then, and resolve on their own.
	if err := fsutil.WriteFile(filepath.Join(at.gen, "snapshot.json"), b, 0o600); err != nil {
		return seat{}, s, err
	}
	return at, s, create(filepath.Join(at.gen, fmt.Sprintf("slot.%d.0", slot)))
}

// join returns where slot joins generation gen, and its snapshot, when the
// snapshot was written or last read at most joinWindow ago; the snapshot's
// modification time is when. Joining sets it.
func join(gen string, slot int) (seat, snapshot, bool) {
	var s snapshot
	path := filepath.Join(gen, "snapshot.json")
	st, err := os.Lstat(path)
	if err != nil {
		return seat{}, s, false
	}
	if age := time.Since(st.ModTime()); age < 0 || age > joinWindow {
		return seat{}, s, false
	}
	b, err := fsutil.ReadBounded(path, maxSnapshot)
	if err != nil || json.Unmarshal(b, &s) != nil {
		return seat{}, s, false
	}
	now := time.Now()
	if os.Chtimes(path, now, now) != nil {
		return seat{}, s, false
	}
	at := seat{gen: gen}
	for {
		if _, err := os.Lstat(filepath.Join(gen, fmt.Sprintf("slot.%d.%d", slot, at.round))); errors.Is(err, os.ErrNotExist) {
			break
		}
		at.round++
	}
	if at.round > 0 {
		others, _ := filepath.Glob(filepath.Join(gen, fmt.Sprintf("slot.*.%d", at.round)))
		at.first = len(others) == 0
	}
	return at, s, create(filepath.Join(gen, fmt.Sprintf("slot.%d.%d", slot, at.round))) == nil
}

// create creates the empty file path, which must not exist.
func create(path string) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	return f.Close()
}

// generations are the names of the generation folders in d, oldest first.
func generations(d string) []string {
	entries, _ := os.ReadDir(d)
	var out []string
	for _, e := range entries {
		if e.IsDir() && len(e.Name()) == 16 {
			out = append(out, e.Name())
		}
	}
	return out
}

// clean deletes the generations in d, and the folders next to d, that no
// slot used for keepSlots: a pin slides with every read, so a generation
// can be older than that and still pinned, but its last use isn't. All its
// slots have ended by then, because the agents end a hook after 10 s. The
// caller holds d's lock; the folder of another session start is checked
// and deleted only while ctxtra holds its lock, and moved away first, so a
// slot that opened its lock before doesn't use it (see fsutil.LockTimeout).
func clean(d string, gens []string) {
	for _, g := range gens {
		gen := filepath.Join(d, g)
		used := lastUse(gen)
		// A generation's own time counts: it is new before its snapshot is.
		if st, err := os.Lstat(gen); err == nil && st.ModTime().After(used) {
			used = st.ModTime()
		}
		if time.Since(used) > keepSlots {
			os.RemoveAll(gen)
		}
	}
	parent := filepath.Dir(d)
	entries, _ := os.ReadDir(parent)
	for _, e := range entries {
		other := filepath.Join(parent, e.Name())
		if other == d || !e.IsDir() || strings.HasPrefix(e.Name(), ".") || time.Since(lastUse(other)) <= keepSlots {
			continue
		}
		unlock, err := fsutil.LockTimeout(filepath.Join(other, "lock"), 0)
		if err != nil {
			continue
		}
		// Used since the check above: a slot joined while ctxtra waited.
		if time.Since(lastUse(other)) <= keepSlots {
			unlock()
			continue
		}
		trash := filepath.Join(parent, fmt.Sprintf(".deleted-%s-%d", e.Name(), os.Getpid()))
		moved := os.Rename(other, trash) == nil
		unlock()
		if moved {
			os.RemoveAll(trash)
		}
	}
}

// lastUse is the newest modification time of what is in the folder dir and
// in its folders: a read of a snapshot sets its time, and a slot that joins
// makes files. The lock files don't count, nor does dir itself, whose time
// changes when clean makes its lock to check it.
func lastUse(dir string) time.Time {
	var last time.Time
	filepath.WalkDir(dir, func(path string, e fs.DirEntry, err error) error {
		if err != nil || path == dir || e.Name() == "lock" {
			return nil
		}
		if info, err := e.Info(); err == nil && info.ModTime().After(last) {
			last = info.ModTime()
		}
		if e.IsDir() && path != dir && filepath.Dir(path) != dir {
			return filepath.SkipDir
		}
		return nil
	})
	return last
}

// waitFor waits until the slot before has answered, which creates done,
// and ended, which frees its lock run, or until deadline.
func waitFor(done, run string, deadline time.Time) {
	for {
		if _, err := os.Lstat(done); err == nil {
			break
		}
		if time.Now().After(deadline) {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	// The slot holds run until it ends.
	if unlock, err := fsutil.LockTimeout(run, time.Until(deadline)); err == nil {
		unlock()
		time.Sleep(orderGap)
	}
}

// finish marks slot as done in its round, for the order of the slot after
// it. The snapshot stays: a slot may still join it while it is pinned, and
// clean deletes it with its generation once no slot used it for keepSlots.
func finish(at seat, slot int) {
	if f, err := os.OpenFile(filepath.Join(at.gen, fmt.Sprintf("done.%d.%d", slot, at.round)), os.O_CREATE|os.O_WRONLY, 0o600); err == nil {
		f.Close()
	}
}
