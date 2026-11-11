package fsutil

import "errors"

// ErrLockTimeout is LockShared or LockTimeout giving up.
var ErrLockTimeout = errors.New("timed out waiting for a lock")
