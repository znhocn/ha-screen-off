//go:build linux || darwin

package daemon

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
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

// processStartUnixSec returns the epoch second at which pid started, or 0 when
// it cannot be determined. Only Linux implements it (/proc); on darwin there
// is no portable way to get a start time without cgo, so it returns 0 and the
// pid-reuse guard in Stop simply stays disabled there.
func processStartUnixSec(pid int) int64 {
	if runtime.GOOS != "linux" {
		return 0
	}
	btime := bootSec()
	if btime <= 0 {
		return 0
	}
	stat, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return 0
	}
	s := string(stat)
	end := strings.LastIndexByte(s, ')')
	if end < 0 {
		return 0
	}
	// After ")" the fields restart at field 3 (state); starttime is field 22,
	// i.e. index 22-3 = 19 within the tail.
	fields := strings.Fields(s[end+1:])
	if len(fields) <= 19 {
		return 0
	}
	startTicks, err := strconv.ParseInt(fields[19], 10, 64)
	if err != nil {
		return 0
	}
	// Field 22 is in clock ticks (USER_HZ = 100 Hz on Linux).
	return btime + startTicks/100
}

// bootSec returns the kernel boot time (epoch seconds) from /proc/stat.
func bootSec() int64 {
	data, err := os.ReadFile("/proc/stat")
	if err != nil {
		return 0
	}
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, "btime ") {
			sec, err := strconv.ParseInt(strings.TrimSpace(strings.TrimPrefix(line, "btime ")), 10, 64)
			if err == nil {
				return sec
			}
		}
	}
	return 0
}
