//go:build unix

package fsutil

import "syscall"

// openNonblock keeps opening a FIFO from hanging; ReadBounded refuses it after.
const openNonblock = syscall.O_NONBLOCK
