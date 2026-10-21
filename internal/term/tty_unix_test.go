//go:build darwin || dragonfly || freebsd || linux || netbsd || openbsd

package term

import (
	"os"
	"testing"
)

// /dev/null is a character device, but not a terminal.
func TestDevNullIsNoTerminal(t *testing.T) {
	f, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if IsTerminal(f) {
		t.Error("/dev/null is a terminal")
	}
}
