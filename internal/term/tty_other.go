//go:build !(darwin || dragonfly || freebsd || linux || netbsd || openbsd || windows)

package term

import "os"

// IsTerminal reports whether f is a character device, which a terminal is,
// but also a device such as /dev/null.
func IsTerminal(f *os.File) bool {
	st, err := f.Stat()
	return err == nil && st.Mode()&os.ModeCharDevice != 0
}
