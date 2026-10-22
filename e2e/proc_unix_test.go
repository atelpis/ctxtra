//go:build unix

package e2e

import (
	"os/exec"
	"syscall"
	"time"
)

// setProcessGroup makes a timeout kill the agent's whole process tree, not
// just the agent: agents start shells, hooks and subagents.
func setProcessGroup(c *exec.Cmd) {
	c.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	c.Cancel = func() error { return syscall.Kill(-c.Process.Pid, syscall.SIGKILL) }
}

// endGroup waits until the process group pgid is gone, and kills it if it
// is still there after 10 seconds: an agent that tmux hung up on can go on
// writing into the fixture for a moment.
func endGroup(pgid int) {
	gone := func(d time.Duration) bool {
		for end := time.Now().Add(d); time.Now().Before(end); time.Sleep(100 * time.Millisecond) {
			if syscall.Kill(-pgid, 0) == syscall.ESRCH {
				return true
			}
		}
		return false
	}
	if !gone(10 * time.Second) {
		syscall.Kill(-pgid, syscall.SIGKILL)
		gone(5 * time.Second)
	}
}
