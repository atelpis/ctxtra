//go:build unix

package fsutil

import (
	"os"
	"path/filepath"
	"syscall"
)

// Lock takes an exclusive lock on the file path, creating it, and waits
// until it gets it. The lock is released by the returned function, or when
// the process ends.
func Lock(path string) (unlock func(), err error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		f.Close()
		return nil, err
	}
	return func() { f.Close() }, nil
}
