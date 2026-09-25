//go:build windows

// Package winsys provides Windows console/window helpers used by scroff.
// Non-Windows builds get a no-op implementation.
package winsys

import (
	"syscall"
	"time"
	"unsafe"
)

const maxConsoleProcs = 64

var (
	kernel32                = syscall.NewLazyDLL("kernel32.dll")
	user32                  = syscall.NewLazyDLL("user32.dll")
	procConsoleWnd          = kernel32.NewProc("GetConsoleWindow")
	procShowWindow          = user32.NewProc("ShowWindow")
	procGetConsoleProcesses = kernel32.NewProc("GetConsoleProcessList")
)

// HideIfOwned hides the console window when it was created just for this
// process (launched by Task Scheduler or by double-click), so no black window
// sits on the desktop while scroff runs as a watchdog. When scroff runs inside
// an existing terminal the console is shared and left alone. force hides
// regardless of ownership (the -hide-console flag). It reports whether the
// window was actually hidden, so the caller can go fully silent (a hidden
// console has no remaining output surface).
func HideIfOwned(force bool) bool {
	if !force && consoleProcessCount() != 1 {
		return false
	}
	hwnd, _, _ := procConsoleWnd.Call()
	if hwnd == 0 { // no console window belongs to this process
		return false
	}
	procShowWindow.Call(hwnd, 0) // SW_HIDE
	return true
}

// consoleProcessCount reports how many processes are attached to this console
// (0 when the process has no console at all). A count of 1 means the console
// was freshly created for this process only.
func consoleProcessCount() int {
	var pids [maxConsoleProcs]uint32
	n, _, _ := procGetConsoleProcesses.Call(uintptr(unsafe.Pointer(&pids[0])), maxConsoleProcs)
	return int(n)
}

// KeepHidden re-asserts SW_HIDE on the console window until the process exits.
// Some Windows builds re-display a console that was hidden if the process
// afterwards writes to it; this makes a hidden-headless watchdog stay hidden.
// Only called after HideIfOwned reported a hide (a shared console is never
// touched). Runs until process exit.
func KeepHidden() {
	hwnd, _, _ := procConsoleWnd.Call()
	if hwnd == 0 {
		return
	}
	for {
		time.Sleep(500 * time.Millisecond)
		procShowWindow.Call(hwnd, 0) // SW_HIDE
	}
}
