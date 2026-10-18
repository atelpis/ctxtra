//go:build unix

package fsutil

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"time"
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

// ErrLockTimeout is LockShared or LockTimeout giving up.
var ErrLockTimeout = errors.New("timed out waiting for a lock")

// LockShared takes a shared lock on the file path: many holders, but none
// while someone holds Lock. It waits at most timeout. Where the file can't
// be written, for example in an agent's sandbox, an existing one is locked
// read-only: flock doesn't need write access.
func LockShared(path string, timeout time.Duration) (unlock func(), err error) {
	return lockWait(path, syscall.LOCK_SH, timeout)
}

// LockTimeout is Lock that waits at most timeout.
func LockTimeout(path string, timeout time.Duration) (unlock func(), err error) {
	return lockWait(path, syscall.LOCK_EX, timeout)
}

// lockWait locks path, and again when the file it locked is no longer at
// path, deleted or moved by someone who held the lock before: holders of
// the old file and of a new one would exclude each other no more.
func lockWait(path string, how int, timeout time.Duration) (unlock func(), err error) {
	deadline := time.Now().Add(timeout)
	for {
		f, err := lockFile(path, how, deadline)
		if err != nil {
			return nil, err
		}
		fst, err1 := f.Stat()
		st, err2 := os.Stat(path)
		if err1 == nil && err2 == nil && os.SameFile(fst, st) {
			return func() { f.Close() }, nil
		}
		f.Close()
		if time.Now().After(deadline) {
			return nil, ErrLockTimeout
		}
	}
}

func lockFile(path string, how int, deadline time.Time) (*os.File, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if errors.Is(err, os.ErrPermission) {
		f, err = os.Open(path)
	}
	if err != nil {
		return nil, err
	}
	for {
		err := syscall.Flock(int(f.Fd()), how|syscall.LOCK_NB)
		if err == nil {
			return f, nil
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) || time.Now().After(deadline) {
			f.Close()
			if errors.Is(err, syscall.EWOULDBLOCK) {
				err = ErrLockTimeout
			}
			return nil, err
		}
		time.Sleep(5 * time.Millisecond)
	}
}
