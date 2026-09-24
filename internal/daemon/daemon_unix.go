//go:build linux || darwin

package daemon

import (
	"fmt"
	"os"
	"os/exec"
	"syscall"
)

// acquireLock takes a non-blocking exclusive flock on path. A second process
// holding the lock fails with EWOULDBLOCK; the lock is released when the file
// is closed (including on crash/kill).
func acquireLock(path string) (*os.File, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = f.Close()
		return nil, err
	}
	return f, nil
}

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
