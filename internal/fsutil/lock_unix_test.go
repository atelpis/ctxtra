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

// A lock whose file was moved away while someone waited for it, as slot
// cleanup does, isn't held: the waiter locks the new file at the path,
// which another holder excludes.
func TestLockMoved(t *testing.T) {
	d := filepath.Join(t.TempDir(), "d")
	p := filepath.Join(d, "lock")
	first, err := LockTimeout(p, 0)
	if err != nil {
		t.Fatal(err)
	}
	got := make(chan func(), 1)
	go func() {
		if u, err := LockTimeout(p, 3*time.Second); err == nil {
			got <- u
		}
		close(got)
	}()
	time.Sleep(50 * time.Millisecond) // the waiter has the old file open
	if err := os.Rename(d, d+".old"); err != nil {
		t.Fatal(err)
	}
	other, err := LockTimeout(p, 0)
	if err != nil {
		t.Fatal(err)
	}
	first()
	select {
	case <-got:
		t.Fatal("got the lock of a moved file while another holds the file at the path")
	case <-time.After(200 * time.Millisecond):
	}
	other()
	if u, ok := <-got; !ok {
		t.Fatal("no lock after the holder released it")
	} else {
		u()
	}
}
