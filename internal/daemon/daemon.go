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
	defaultPerm = 0o755
)

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

// WritePID stores this process's pid (only meaningful inside the daemon).
func WritePID() error {
	path, err := PIDFile()
	if err != nil {
		return err
	}
	return os.WriteFile(path, []byte(strconv.Itoa(os.Getpid())), 0o644)
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
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		return 0, fmt.Errorf("corrupt pid file %s: %w", path, err)
	}
	return pid, nil
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
	logPath, err := LogFile()
	if err != nil {
		return 0, err
	}
	dir, _ := RootDir()
	logf, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return 0, fmt.Errorf("open log file %s: %w", logPath, err)
	}
	defer logf.Close()

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

	// Wait for the child to write its pid file, confirming successful startup.
	pid := cmd.Process.Pid
	deadline := time.Now().Add(5 * time.Second)
	for {
		if stored, err := ReadPID(); err == nil && stored == pid {
			// Give the child a short grace period, then confirm it is still
			// alive. This catches crashes shortly after the pid file was written.
			time.Sleep(500 * time.Millisecond)
			if err := processAlive(pid); err != nil {
				return 0, fmt.Errorf("background process exited after startup; last log lines:\n%s", tail(logPath, 8))
			}
			return pid, nil
		}
		if !time.Now().Before(deadline) {
			return 0, fmt.Errorf("timeout waiting for background process; last log lines:\n%s", tail(logPath, 8))
		}
		// Child exited before writing its pid file?
		if err := processAlive(pid); err != nil {
			return 0, fmt.Errorf("background process exited immediately; last log lines:\n%s", tail(logPath, 8))
		}
		time.Sleep(200 * time.Millisecond)
	}
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

// Stop terminates the running daemon and removes its pid file.
func Stop() (int, error) {
	pid, err := ReadPID()
	if err != nil {
		return 0, err
	}
	if err := terminate(pid); err != nil {
		return 0, fmt.Errorf("stop pid %d: %w", pid, err)
	}
	_ = RemovePID()
	return pid, nil
}
