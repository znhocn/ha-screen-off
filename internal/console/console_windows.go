//go:build windows

// Package console bridges the GUI-subsystem (-H=windowsgui) Windows build
// back to a terminal. scroff is deliberately built as a GUI-subsystem
// executable so it NEVER opens a console window on its own - a scheduled-task
// or autostart run is windowless and silent from the start. When launched
// interactively from a terminal, Attach connects to the parent's console and
// redirects standard I/O to it, so normal command-line output still works.
package console

import (
	"os"
	"syscall"
)

// attachParentProcess tells AttachConsole to attach to the parent process's
// console (0xFFFFFFFF).
const attachParentProcess = ^uintptr(0)

var procAttachConsole = syscall.NewLazyDLL("kernel32.dll").NewProc("AttachConsole")

// Attach attaches this process to its parent's console, if one exists, and
// redirects stdin/stdout/stderr to it. It reports whether a console is
// available for output. When launched headless (Task Scheduler, autostart,
// services, Explorer double-click) there is nothing to attach to, so scroff
// stays silent and windowless.
func Attach() bool {
	r, _, _ := procAttachConsole.Call(attachParentProcess)
	if r == 0 {
		return false
	}
	if out, err := os.OpenFile("CONOUT$", os.O_WRONLY, 0); err == nil {
		os.Stdout = out
	}
	if errOut, err := os.OpenFile("CONOUT$", os.O_WRONLY, 0); err == nil {
		os.Stderr = errOut
	}
	if in, err := os.OpenFile("CONIN$", os.O_RDONLY, 0); err == nil {
		os.Stdin = in
	}
	return true
}
