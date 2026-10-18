//go:build unix

package fsutil

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// A lock file that can't be written, as in an agent's sandbox, is still
// locked shared, and still excludes Lock.
func TestLockSharedReadOnly(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root writes read-only files")
	}
	dir := t.TempDir()
	p := filepath.Join(dir, "store.lock")
	if err := os.WriteFile(p, nil, 0o400); err != nil {
		t.Fatal(err)
	}
	os.Chmod(dir, 0o500)
	t.Cleanup(func() { os.Chmod(dir, 0o700) })

	unlock, err := LockShared(p, 0)
	if err != nil {
		t.Fatal(err)
	}
	os.Chmod(p, 0o600)
	done := make(chan struct{})
	go func() {
		u, err := Lock(p)
		if err == nil {
			u()
		}
		close(done)
	}()
	select {
	case <-done:
		t.Fatal("Lock got the lock while a shared lock was held")
	case <-time.After(100 * time.Millisecond):
	}
	unlock()
	<-done
}
