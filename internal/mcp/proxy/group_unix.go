//go:build unix

package proxy

import (
	"os/exec"
	"syscall"
)

// setGroup starts cmd in a process group of its own, so that it can be
// stopped with the processes it starts.
func setGroup(cmd *exec.Cmd) { cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true} }

// signalGroup sends SIGTERM, or SIGKILL with kill, to the process group.
func signalGroup(pgid int, kill bool) {
	sig := syscall.SIGTERM
	if kill {
		sig = syscall.SIGKILL
	}
	_ = syscall.Kill(-pgid, sig)
}

// groupAlive reports whether a process of the group is still there.
func groupAlive(pgid int) bool { return syscall.Kill(-pgid, 0) == nil }
