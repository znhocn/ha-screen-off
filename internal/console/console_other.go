//go:build !windows

// Package console prepares standard I/O for platform-specific console
// behavior. Non-Windows builds already have working standard I/O.
package console

// Attach is a no-op on non-Windows platforms; it reports that a console is
// available.
func Attach() bool { return true }
