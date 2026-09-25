//go:build windows

package daemon

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"syscall"
	"unsafe"
)

const (
	flagDetached = 0x00000008 // DETACHED_PROCESS
	flagNewGroup = 0x00000200 // CREATE_NEW_PROCESS_GROUP
	flagNoWindow = 0x08000000 // CREATE_NO_WINDOW

	processQueryInfo = 0x0400 // PROCESS_QUERY_INFORMATION
	processQueryLtd  = 0x1000 // PROCESS_QUERY_LIMITED_INFORMATION
)

// epoch1900 is the seconds between the Windows FILETIME epoch (1601-01-01)
// and the Unix epoch (1970-01-01).
const epoch1900 = 11644473600

// setAttributes starts the child without a console window, detached from the
// terminal session. Screen toggling still works as long as the child runs in
// the interactive desktop session (same user, no SSH/service context).
func setAttributes(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: flagNoWindow | flagNewGroup | flagDetached,
	}
}

// acquireLock takes a single-instance lock by opening path with share mode 0
// (no sharing): a second process opening the same file fails with
// ERROR_SHARING_VIOLATION. The lock is released when the handle is closed
// (including on crash/kill).
func acquireLock(path string) (*os.File, error) {
	p, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	h, err := syscall.CreateFile(p,
		syscall.GENERIC_READ|syscall.GENERIC_WRITE,
		0, // share mode: no sharing -> exclusive
		nil,
		syscall.OPEN_ALWAYS,
		syscall.FILE_ATTRIBUTE_NORMAL,
		0)
	if err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(h), path), nil
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

// processStartUnixSec returns the epoch second at which pid started, from the
// creation time in its FILETIME. It feeds the pid-reuse guard in daemon.Stop.
var procGetProcessTimes = syscall.NewLazyDLL("kernel32.dll").NewProc("GetProcessTimes")

// filetime is a Windows FILETIME (100ns ticks since 1601-01-01).
type filetime struct{ lo, hi uint32 }

func processStartUnixSec(pid int) int64 {
	h, err := syscall.OpenProcess(processQueryLtd, false, uint32(pid))
	if err != nil {
		return 0
	}
	defer syscall.CloseHandle(h)
	var creation, exit, kernel, user filetime
	r, _, _ := procGetProcessTimes.Call(uintptr(h),
		uintptr(unsafe.Pointer(&creation)),
		uintptr(unsafe.Pointer(&exit)),
		uintptr(unsafe.Pointer(&kernel)),
		uintptr(unsafe.Pointer(&user)))
	if r == 0 {
		return 0
	}
	ft := uint64(creation.hi)<<32 | uint64(creation.lo)
	return int64(ft/10_000_000) - epoch1900
}
