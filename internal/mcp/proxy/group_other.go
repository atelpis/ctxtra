//go:build !unix

package proxy

import (
	"os"
	"os/exec"
)

// ctxtra ships for Unix only; elsewhere a server is stopped alone.

func setGroup(*exec.Cmd) {}

func signalGroup(pid int, _ bool) {
	if p, err := os.FindProcess(pid); err == nil {
		_ = p.Kill()
	}
}

func groupAlive(int) bool { return false }
