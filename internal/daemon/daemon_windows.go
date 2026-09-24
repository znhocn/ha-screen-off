//go:build windows

package daemon

import (
	"fmt"
	"os/exec"
	"strconv"
	"syscall"
)

const (
	flagDetached = 0x00000008 // DETACHED_PROCESS
	flagNewGroup = 0x00000200 // CREATE_NEW_PROCESS_GROUP
	flagNoWindow = 0x08000000 // CREATE_NO_WINDOW

	processQueryInfo = 0x0400 // PROCESS_QUERY_INFORMATION
)

// setAttributes starts the child without a console window, detached from the
// terminal session. Screen toggling still works as long as the child runs in
// the interactive desktop session (same user, no SSH/service context).
func setAttributes(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: flagNoWindow | flagNewGroup | flagDetached,
	}
}

// terminate stops the daemon. taskkill /F /T /PID force-kills the whole
// process tree; without /F, taskkill refuses to kill a process that is still a
// direct child of another running process (a common case: the daemon was
// spawned from a shell that is still open). The deferred screen-restore
// cleanup does not run on Windows hard kills, so the entity in Home Assistant
// is the source of truth there.
func terminate(pid int) error {
	cmd := exec.Command("taskkill", "/F", "/T", "/PID", strconv.Itoa(pid))
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("taskkill: %w: %s", err, string(out))
	}
	return nil
}

// processAlive reports whether a process is still running (best-effort: try to
// open it without terminating).
func processAlive(pid int) error {
	h, err := syscall.OpenProcess(processQueryInfo, false, uint32(pid))
	if err != nil {
		return fmt.Errorf("process %d not running: %v", pid, err)
	}
	_ = syscall.CloseHandle(h)
	return nil
}
