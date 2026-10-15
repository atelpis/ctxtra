//go:build unix

package e2e

import (
	"os/exec"
	"syscall"
)

// setProcessGroup makes a timeout kill the agent's whole process tree, not
// just the agent: agents start shells, hooks and subagents.
func setProcessGroup(c *exec.Cmd) {
	c.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	c.Cancel = func() error { return syscall.Kill(-c.Process.Pid, syscall.SIGKILL) }
}
