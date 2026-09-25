//go:build !windows

// Package console bridges the GUI-subsystem Windows build back to a
// terminal. Non-Windows builds are ordinary console programs, so Attach is a
// no-op: the process already has standard I/O attached.
package console

// Attach is a no-op on non-Windows platforms; it reports that a console is
// available.
func Attach() bool { return true }
