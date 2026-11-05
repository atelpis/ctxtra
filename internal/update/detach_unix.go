//go:build unix

package update

import (
	"os/exec"
	"syscall"
	"testing"
)

// start runs the check in its own session: an agent that ends the hook's
// process group when the hook returns doesn't end the check. Without
// Stdin, Stdout and Stderr the check gets /dev/null, so it holds none of the
// hook's pipes and the agent doesn't wait for it.
func start(args []string) error {
	// A test binary would run its tests instead.
	if testing.Testing() {
		return nil
	}
	cmd := exec.Command(executable(), args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}
