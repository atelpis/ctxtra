package fsutil

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestIncompleteBackupRemoved(t *testing.T) {
	for _, phase := range []string{"write", "close"} {
		t.Run(phase, func(t *testing.T) {
			dir := t.TempDir()
			f, err := os.CreateTemp(dir, "backup-*.tmp")
			if err != nil {
				t.Fatal(err)
			}
			fault := errors.New("backup failure")
			if err := finishBackup(&failingBackup{File: f, phase: phase, err: fault}, []byte("complete content")); !errors.Is(err, fault) {
				t.Fatalf("want backup failure, got %v", err)
			}
			entries, err := os.ReadDir(dir)
			if err != nil || len(entries) != 0 {
				t.Fatalf("incomplete backup left behind: %v, %v", entries, err)
			}
		})
	}
}

type failingBackup struct {
	*os.File
	phase string
	err   error
}

func (f *failingBackup) Write(data []byte) (int, error) {
	if f.phase == "write" {
		n, _ := f.File.Write(data[:len(data)/2])
		return n, f.err
	}
	return f.File.Write(data)
}

func (f *failingBackup) Close() error {
	err := f.File.Close()
	if f.phase == "close" {
		return f.err
	}
	return err
}

func TestBackupClockRollback(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	backups := filepath.Join(dir, "backups")
	for _, content := range []string{"one", "two", "three"} {
		if err := Backup(path, []byte(content), backups); err != nil {
			t.Fatal(err)
		}
	}
	prefix, err := backupPrefix(path)
	if err != nil {
		t.Fatal(err)
	}
	names, err := backupNames(prefix, backups)
	if err != nil {
		t.Fatal(err)
	}
	future := prefix + "20990101-000000.000000000.1.bak"
	if err := os.Rename(filepath.Join(backups, names[len(names)-1]), filepath.Join(backups, future)); err != nil {
		t.Fatal(err)
	}
	if err := Backup(path, []byte("newest"), backups); err != nil {
		t.Fatal(err)
	}
	if err := PruneBackups(path, backups); err != nil {
		t.Fatal(err)
	}
	contents := backupContents(t, backups)
	if strings.Join(contents, ",") != "newest,three,two" {
		t.Fatalf("clock rollback lost the new backup: %v", contents)
	}
}

func TestWriteAtomicPruneFailureIsNotWriteFailure(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	backups := filepath.Join(dir, "backups")
	for _, content := range []string{"one", "two", "three", "current"} {
		if err := Backup(path, []byte(content), backups); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(path, []byte("current"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(backups, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(backups, 0o700) })
	if f, err := os.CreateTemp(backups, "probe"); err == nil {
		f.Close()
		os.Remove(f.Name())
		t.Skip("directory permissions do not prevent writes for this user")
	}
	if err := PruneBackups(path, backups); err == nil {
		t.Fatal("pruning unexpectedly succeeded")
	}
	if _, changed, err := WriteAtomic(path, []byte("new"), backups, false); err != nil || !changed {
		t.Fatalf("committed write reported as failed: changed=%v, err=%v", changed, err)
	}
	if got, err := os.ReadFile(path); err != nil || string(got) != "new" {
		t.Fatalf("config not written: %q, %v", got, err)
	}
	if got := backupContents(t, backups); len(got) != 4 {
		t.Fatalf("prune failure did not preserve backups: %v", got)
	}
}

func TestBackupLongName(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, strings.Repeat("x", 240))
	backups := filepath.Join(dir, "backups")
	if err := Backup(path, []byte("original"), backups); err != nil {
		t.Fatalf("long config name prevented backup: %v", err)
	}
}
