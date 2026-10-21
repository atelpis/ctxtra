package term

import (
	"os"

	"golang.org/x/sys/windows"
)

// IsTerminal reports whether f is a console.
func IsTerminal(f *os.File) bool {
	var mode uint32
	return windows.GetConsoleMode(windows.Handle(f.Fd()), &mode) == nil
}
