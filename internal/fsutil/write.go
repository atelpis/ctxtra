package fsutil

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// WriteAtomic writes data to path when the content changes, and reports
// whether it did (or would, with dryRun). The old file is first copied to a
// new, uniquely named file in backupDir. The file keeps its mode. A symlink
// (for example into a dotfiles repo) stays a symlink: its target is written.
func WriteAtomic(path string, data []byte, backupDir string, dryRun bool) (string, bool, error) {
	if st, err := os.Lstat(path); err == nil && st.Mode()&os.ModeSymlink != 0 {
		target, err := filepath.EvalSymlinks(path)
		if err != nil {
			return path, false, fmt.Errorf("%s is a symlink to a missing file; left alone", path)
		}
		path = target
	}
	old, err := os.ReadFile(path)
	exists := err == nil
	if exists && bytes.Equal(old, data) {
		return path, false, nil
	}
	if dryRun {
		return path, true, nil
	}
	mode := os.FileMode(0o644)
	if exists {
		if st, err := os.Stat(path); err == nil {
			mode = st.Mode().Perm()
		}
		if err := Backup(path, old, backupDir); err != nil {
			return path, false, err
		}
	}
	return path, true, replace(path, data, mode)
}

// Backup writes old, the content of path, to a new file in dir named after
// the path and the time. It never overwrites an earlier backup.
func Backup(path string, old []byte, dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	prefix := fmt.Sprintf("%s-%s.%s.", strings.TrimPrefix(filepath.Base(filepath.Dir(path)), "."),
		filepath.Base(path), time.Now().Format("20060102-150405"))
	f, err := os.CreateTemp(dir, prefix+"*.bak") // mode 0600
	if err != nil {
		return err
	}
	if _, err := f.Write(old); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// replace writes data to a temporary file next to path and renames it, so a
// reader never sees a half-written file.
func replace(path string, data []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".ctxtra-")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmp.Name(), mode); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
