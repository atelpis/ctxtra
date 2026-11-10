// Package status keeps, per session folder, what the proxy saw at the last
// session start there: which bound servers had their tools in the first
// tool list, which came later, and which were missing and why. It holds
// no secret, and is written without the credential lock.
package status

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"

	"github.com/ctxtra/ctxtra/internal/fsutil"
	"github.com/ctxtra/ctxtra/internal/paths"
)

// version is the format of the files this ctxtra writes.
const version = 1

// Server states.
const (
	Ready   = "ready"   // its tools were in the first tool list
	Late    = "late"    // it answered after the first tool list
	Missing = "missing" // it has no tools in the session
)

// Record is one session start in a folder.
type Record struct {
	Version int       `json:"version"`
	Dir     string    `json:"dir"`
	Agent   string    `json:"agent"`
	Started time.Time `json:"started"`
	// Deadline is how long the proxy waited for the first tool list.
	DeadlineMs int64    `json:"deadline_ms"`
	Servers    []Server `json:"servers"`
}

// Server is what happened to one bound server.
type Server struct {
	Name  string `json:"name"`
	State string `json:"state"`
	// Reason says why it is missing, or late; Ms is how long it took to
	// answer, 0 when it never did.
	Reason string `json:"reason,omitempty"`
	Ms     int64  `json:"ms,omitempty"`
}

// Dir is where the records are.
func Dir() string { return filepath.Join(paths.StateDir(), "mcp", "status") }

// path is the record of the session folder dir: one file per folder, so
// sessions in other folders never touch it.
func path(dir string) string {
	// The folder without symlinks, as bindings see it.
	sum := sha256.Sum256([]byte(paths.Real(dir)))
	return filepath.Join(Dir(), hex.EncodeToString(sum[:16])+".json")
}

// Write replaces the record of r.Dir, unless it holds a later session's
// start: a slow server of an older session in the folder must not hide
// the newer one.
func Write(r Record) error {
	r.Version = version
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	p := path(r.Dir)
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	unlock, err := fsutil.LockTimeout(p+".lock", 2*time.Second)
	if err != nil {
		return err
	}
	defer unlock()
	if cur, ok, _ := Read(r.Dir); ok && cur.Started.After(r.Started) {
		return nil
	}
	return fsutil.WriteFile(p, append(data, '\n'), 0o600)
}

// Read is the record of the session folder dir; ok is false without one.
func Read(dir string) (Record, bool, error) {
	data, err := fsutil.ReadBounded(path(dir), 1<<20)
	if errors.Is(err, os.ErrNotExist) {
		return Record{}, false, nil
	}
	if err != nil {
		return Record{}, false, err
	}
	var r Record
	if err := json.Unmarshal(data, &r); err != nil {
		return Record{}, false, err
	}
	if r.Version > version || paths.Real(r.Dir) != paths.Real(dir) {
		return Record{}, false, nil
	}
	return r, true, nil
}
