//go:build linux || darwin

package daemon

import (
	"fmt"
	"os/exec"
	"syscall"
)

// setAttributes detaches the child from the controlling terminal.
func setAttributes(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}

// terminate asks the daemon to shut down gracefully (SIGTERM runs the
// screen-restore cleanup in the child).
func terminate(pid int) error {
	return syscall.Kill(pid, syscall.SIGTERM)
}

// processAlive reports whether a process is still running.
func processAlive(pid int) error {
	if err := syscall.Kill(pid, 0); err != nil {
		return fmt.Errorf("process %d not running: %v", pid, err)
	}
	return nil
}
