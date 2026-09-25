//go:build !windows

// Package winsys provides Windows console/window helpers used by scroff.
// Non-Windows builds get a no-op implementation.
package winsys

// HideIfOwned is a no-op on non-Windows platforms; it reports that nothing
// was hidden.
func HideIfOwned(force bool) bool { return false }

// KeepHidden is a no-op on non-Windows platforms.
func KeepHidden() {}
