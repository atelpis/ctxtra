package fsutil

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"
)

const backupTimeFormat = "20060102-150405.000000000"

// WriteAtomic writes data to path when the content changes, and reports
// whether it did (or would, with dryRun). The old content is backed up first;
// an identical latest backup is reused. The file keeps its mode. A symlink
// (for example into a dotfiles repo) stays a symlink: its target is written.
// After a successful write, older backups of that path are pruned on a best-effort basis.
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
	if err := replace(path, data, mode, 0o755); err != nil {
		return path, true, err
	}
	if exists {
		// Cleanup must not turn a committed config change into a failed operation.
		_ = PruneBackups(path, backupDir)
	}
	return path, true, nil
}

// Backup saves old, the content of path, unless the latest backup already
// has that content. It never overwrites an earlier backup.
func Backup(path string, old []byte, dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	prefix, err := backupPrefix(path)
	if err != nil {
		return err
	}
	names, err := backupNames(prefix, dir)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	if len(names) > 0 {
		latest := names[len(names)-1]
		if data, err := ReadBounded(filepath.Join(dir, latest), len(old)); err == nil && bytes.Equal(data, old) {
			return nil
		}
		stamp := strings.TrimPrefix(latest, prefix)[:len(backupTimeFormat)]
		last, _ := time.Parse(backupTimeFormat, stamp)
		// Keep creation order even when the clock moves backward.
		if !now.After(last) {
			now = last.Add(time.Nanosecond)
		}
	}
	f, err := os.CreateTemp(dir, prefix+now.Format(backupTimeFormat)+".*.tmp") // mode 0600
	if err != nil {
		return err
	}
	return finishBackup(f, old)
}

func finishBackup(f interface {
	Write([]byte) (int, error)
	Close() error
	Name() string
}, old []byte) error {
	complete := false
	defer func() {
		if !complete {
			_ = os.Remove(f.Name())
		}
	}()
	if _, err := f.Write(old); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Rename(f.Name(), strings.TrimSuffix(f.Name(), ".tmp")+".bak"); err != nil {
		return err
	}
	complete = true
	return nil
}

// backupPrefix identifies the absolute path, not just its last two components.
// Older names lack this identity and cannot safely be pruned.
func backupPrefix(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	parent, err := filepath.EvalSymlinks(filepath.Dir(abs))
	if err != nil {
		return "", err
	}
	abs = filepath.Join(parent, filepath.Base(abs))
	label := strings.TrimPrefix(filepath.Base(parent), ".") + "-" + filepath.Base(abs)
	label = strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '.' || r == '-' || r == '_' {
			return r
		}
		return '_'
	}, label)
	if len(label) > 64 {
		label = label[:64]
	}
	return fmt.Sprintf("config-%x.%s.", sha256.Sum256([]byte(abs)), label), nil
}

// PruneBackups keeps the latest three backups of path. Call it only after
// the config was successfully replaced or removed. Legacy backups, import
// folders, symlinks and unrelated files are left alone.
func PruneBackups(path, dir string) error {
	prefix, err := backupPrefix(path)
	if err != nil {
		return err
	}
	names, err := backupNames(prefix, dir)
	if err != nil {
		return err
	}
	var errs []error
	for _, name := range names[:max(0, len(names)-3)] {
		if err := os.Remove(filepath.Join(dir, name)); err != nil && !errors.Is(err, os.ErrNotExist) {
			errs = append(errs, fmt.Errorf("prune config backups: %w", err))
		}
	}
	return errors.Join(errs...)
}

func backupNames(prefix, dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var names []string
	for _, entry := range entries {
		if !entry.Type().IsRegular() || !strings.HasPrefix(entry.Name(), prefix) {
			continue
		}
		suffix := strings.TrimPrefix(entry.Name(), prefix)
		if len(suffix) <= len(backupTimeFormat) || suffix[len(backupTimeFormat)] != '.' || !strings.HasSuffix(suffix, ".bak") {
			continue
		}
		if _, err := time.Parse(backupTimeFormat, suffix[:len(backupTimeFormat)]); err != nil {
			continue
		}
		random := strings.TrimSuffix(suffix[len(backupTimeFormat)+1:], ".bak")
		if _, err := strconv.ParseUint(random, 10, 32); err != nil {
			continue
		}
		names = append(names, entry.Name())
	}
	// Fixed-width timestamps sort chronologically; the random suffix breaks ties.
	slices.Sort(names)
	return names, nil
}

// replace writes data to a temporary file next to path and renames it, so a
// reader never sees a half-written file. Missing folders get dirMode.
func replace(path string, data []byte, mode, dirMode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), dirMode); err != nil {
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

// WriteFile writes data to path atomically, with mode, without a backup.
// Missing folders are created readable by the user only when the file is.
func WriteFile(path string, data []byte, mode os.FileMode) error {
	dirMode := os.FileMode(0o755)
	if mode&0o077 == 0 {
		dirMode = 0o700
	}
	return replace(path, data, mode, dirMode)
}
