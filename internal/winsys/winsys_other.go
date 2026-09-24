//go:build !windows

// Package winsys provides Windows console/window helpers used by scroff.
// Non-Windows builds get a no-op implementation.
package winsys

// HideConsoleWindow is a no-op on non-Windows platforms.
func HideConsoleWindow() {}
