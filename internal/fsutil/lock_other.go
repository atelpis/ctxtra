//go:build !unix

package fsutil

import "time"

// Lock doesn't lock on systems without flock; ctxtra doesn't support them
// yet.
func Lock(path string) (unlock func(), err error) { return func() {}, nil }

// LockShared doesn't lock either.
func LockShared(path string, timeout time.Duration) (unlock func(), err error) { return func() {}, nil }
