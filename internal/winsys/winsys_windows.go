//go:build windows

// Package winsys provides Windows console/window helpers used by scroff.
// Non-Windows builds get a no-op implementation.
package winsys

import "syscall"

var (
	kernel32       = syscall.NewLazyDLL("kernel32.dll")
	user32         = syscall.NewLazyDLL("user32.dll")
	procConsoleWnd = kernel32.NewProc("GetConsoleWindow")
	procShowWindow = user32.NewProc("ShowWindow")
)

// HideConsoleWindow hides the console window this process was launched with,
// so a Task Scheduler logon task does not pop a black window. Intended to be
// called (via the -hide-console flag) for exactly that scenario; pass it only
// when scroff starts without an interactive terminal.
func HideConsoleWindow() {
	hwnd, _, _ := procConsoleWnd.Call()
	if hwnd == 0 { // no console window belongs to this process
		return
	}
	procShowWindow.Call(hwnd, 0) // SW_HIDE
}
