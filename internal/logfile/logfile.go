// Package logfile appends records to ctxtra's log ($CTXTRA_LOG or
// ~/.ctxtra/log.jsonl), one JSON object per line.
package logfile

import (
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/ctxtra/ctxtra/internal/fsutil"
	"github.com/ctxtra/ctxtra/internal/paths"
)

// maxBytes is when the log is rotated to log.jsonl.1.
const maxBytes = 1 << 20

// Append adds v to the log. Errors are ignored and nothing blocks (the log
// can't be a FIFO): logging must never break a session.
func Append(v any) {
	path := paths.LogPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return
	}
	if st, err := os.Lstat(path); err == nil && st.Mode().IsRegular() && st.Size() > maxBytes {
		_ = os.Rename(path, path+".1")
	}
	f, err := fsutil.OpenRegular(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer f.Close()
	b, err := json.Marshal(v)
	if err != nil {
		return
	}
	_, _ = f.Write(append(b, '\n'))
}
