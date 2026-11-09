package mcptest

import (
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
)

func ignoreTerm() { signal.Ignore(syscall.SIGTERM) }

// startChild starts a process in this one's group that ignores SIGTERM,
// and writes its process ID to pidfile.
func startChild(pidfile string) {
	cmd := exec.Command("/bin/sh", "-c", "trap '' TERM; while :; do sleep 1; done")
	if cmd.Start() == nil {
		_ = os.WriteFile(pidfile, []byte(fmt.Sprint(cmd.Process.Pid)), 0o644)
	}
}
