// Package daemon implements background (daemon) running for `serve`:
// it re-executes the current binary detached from the terminal, redirects its
// output to ~/.config/scroff/scroff.log and records its PID so
// `scroff stop` can terminate it later.
package daemon

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// envChild marks processes spawned as the background daemon.
const envChild = "SCROFF_DAEMON"

const (
	logName     = "scroff.log"
	pidName     = "scroff.pid"
	lockName    = "scroff.lock"
	defaultPerm = 0o755
)

// Lock acquires the single-instance lock: only one scroff watchdog may run at
// a time (a Task Scheduler logon trigger can otherwise start several). The
// returned release drops the lock; it is tied to the open file handle (flock
// on Unix, an exclusive share-mode handle on Windows), so a crashed/killed
// process releases it automatically.
func Lock() (release func(), err error) {
	dir, err := RootDir()
	if err != nil {
		return nil, err
	}
	f, err := acquireLock(filepath.Join(dir, lockName))
	if err != nil {
		return nil, fmt.Errorf("another scroff instance is already running (single-instance lock held): %w", err)
	}
	return func() { _ = f.Close() }, nil
}

// RootDir returns the state directory, creating it if needed.
func RootDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("cannot resolve home directory: %w", err)
	}
	dir := filepath.Join(home, ".config", "scroff")
	if err := os.MkdirAll(dir, defaultPerm); err != nil {
		return "", fmt.Errorf("create directory %q: %w", dir, err)
	}
	return dir, nil
}

// LogFile returns the path of the background log file.
func LogFile() (string, error) {
	dir, err := RootDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, logName), nil
}

// OpenLog returns an append-only handle to the background log file. Used by
// -d children (via Spawn) and by silent watchdog runs (scheduled tasks), so
// `scroff logs` always has something to show.
func OpenLog() (*os.File, error) {
	path, err := LogFile()
	if err != nil {
		return nil, err
	}
	return os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
}

// PIDFile returns the path of the background pid file.
func PIDFile() (string, error) {
	dir, err := RootDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, pidName), nil
}

// Child reports whether this process is the spawned background daemon.
func Child() bool {
	return os.Getenv(envChild) == "1"
}

// WritePID stores this process's pid and its start time (epoch seconds).
// The start time lets Stop detect a stale pid file whose number has since
// been reused by an unrelated process. Format: "pid\nstart_sec\n".
func WritePID() error {
	path, err := PIDFile()
	if err != nil {
		return err
	}
	return os.WriteFile(path,
		[]byte(fmt.Sprintf("%d\n%d\n", os.Getpid(), processStartUnixSec(os.Getpid()))), 0o644)
}

// ReadPID returns the pid stored by the running daemon, if any.
func ReadPID() (int, error) {
	path, err := PIDFile()
	if err != nil {
		return 0, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, fmt.Errorf("no background instance running (no pid file at %s)", path)
		}
		return 0, err
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	pid, err := strconv.Atoi(strings.TrimSpace(lines[0]))
	// pid 0 / negative numbers are never written by WritePID (always a real
	// os.Getpid()). They would otherwise make Stop run kill(0/-1, SIGTERM),
	// signalling this process's whole group or everything we have permission
	// for - so treat them as corrupt instead of acting on them.
	if err != nil || pid <= 0 {
		return 0, fmt.Errorf("corrupt pid file %s: invalid pid", path)
	}
	return pid, nil
}

// recordedStartSec returns the process start time saved by WritePID, or 0
// when it is unavailable (older pid files, non-Linux/Windows platforms).
func recordedStartSec() int64 {
	path, err := PIDFile()
	if err != nil {
		return 0
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) < 2 {
		return 0
	}
	sec, err := strconv.ParseInt(strings.TrimSpace(lines[1]), 10, 64)
	if err != nil {
		return 0
	}
	return sec
}

// RemovePID deletes the pid file (ignores not-exist errors).
func RemovePID() error {
	path, err := PIDFile()
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// Spawn re-executes the current binary with the given args as a detached
// background process writing to the log file. It returns the child's pid once
// the child has confirmed it is alive by writing its pid file.
func Spawn(args []string) (int, error) {
	logf, err := OpenLog()
	if err != nil {
		return 0, fmt.Errorf("open log file: %w", err)
	}
	defer logf.Close()
	dir, _ := RootDir()

	exe, err := os.Executable()
	if err != nil {
		return 0, fmt.Errorf("resolve executable: %w", err)
	}
	cmd := exec.Command(exe, args...)
	cmd.Env = append(os.Environ(), envChild+"=1")
	cmd.Stdout = logf
	cmd.Stderr = logf
	cmd.Dir = dir
	setAttributes(cmd)

	if err := cmd.Start(); err != nil {
		return 0, fmt.Errorf("start background process: %w", err)
	}
	// Reap the child asynchronously: an un-reaped (zombie) child would still
	// answer to kill(pid, 0), hiding a quick startup failure from the liveness
	// probe below and forcing the 5s timeout instead of failing fast.
	go func() { _ = cmd.Wait() }()

	// Wait for the child to write its pid file, confirming successful startup.
	pid := cmd.Process.Pid
	deadline := time.Now().Add(5 * time.Second)
	for {
		if stored, err := ReadPID(); err == nil && stored == pid {
			// Give the child a short grace period, then confirm it is still
			// alive. This catches crashes shortly after the pid file was written.
			time.Sleep(500 * time.Millisecond)
			if err := processAlive(pid); err != nil {
				return 0, spawnFail("background process exited after startup", logf.Name())
			}
			return pid, nil
		}
		if !time.Now().Before(deadline) {
			// The child never confirmed startup. Don't leave it running as an
			// orphan that could later grab the single-instance lock behind the
			// user's back; terminate it best-effort before failing.
			_ = terminate(pid)
			return 0, spawnFail("timed out waiting for the background process to fully start", logf.Name())
		}
		// Child exited before writing its pid file?
		if err := processAlive(pid); err != nil {
			return 0, spawnFail("background process exited before writing its pid file", logf.Name())
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// spawnFail builds a Spawn error. A child that dies that fast is usually a
// second concurrent `serve -d` losing the single-instance race, so the common
// case gets a direct message; anything else falls back to the log tail.
func spawnFail(kind, logPath string) error {
	if lockHeld() {
		return fmt.Errorf("another scroff instance is already running (single-instance lock is held)")
	}
	return fmt.Errorf("%s; last log lines:\n%s", kind, tail(logPath, 8))
}

// lockHeld reports whether another scroff watchdog currently holds the
// single-instance lock.
func lockHeld() bool {
	dir, err := RootDir()
	if err != nil {
		return false
	}
	f, err := acquireLock(filepath.Join(dir, lockName))
	if err != nil {
		return true
	}
	_ = f.Close()
	return false
}

// tail returns the last n lines of file, for error reporting.
func tail(path string, n int) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}

// Stop terminates the running watchdog and removes its pid file. A stale pid
// file (left behind by a crash or hard kill such as taskkill /F or a task End)
// is detected and cleaned up instead of erroring. The recorded start time also
// guards against pid reuse: if the number now belongs to an unrelated process,
// Stop refuses to touch it.
func Stop() (int, error) {
	pid, err := ReadPID()
	if err != nil {
		return 0, err
	}
	if err := processAlive(pid); err != nil {
		_ = RemovePID()
		return 0, fmt.Errorf("no running watchdog: pid %d is gone (stale pid file removed)", pid)
	}
	if start := recordedStartSec(); start > 0 {
		if live := processStartUnixSec(pid); live > 0 && absDiff(live, start) > 3 {
			_ = RemovePID()
			return 0, fmt.Errorf("no running watchdog: pid %d belongs to a different process now (stale pid file removed)", pid)
		}
	}
	if err := terminate(pid); err != nil {
		return 0, fmt.Errorf("stop pid %d: %w", pid, err)
	}
	_ = RemovePID()
	return pid, nil
}

// absDiff returns the absolute difference between two epoch seconds.
func absDiff(a, b int64) int64 {
	if a > b {
		return a - b
	}
	return b - a
}
