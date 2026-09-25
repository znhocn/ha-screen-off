//go:build windows

// Package console prepares Windows standard I/O for console and GUI-subsystem
// builds. Console builds wait correctly in PowerShell, while a console created
// only for scroff is detached and silenced.
package console

import (
	"os"
	"syscall"
	"unsafe"
)

const (
	attachParentProcess      = ^uintptr(0)
	disableNewlineAutoReturn = uint32(0x0008)
	maxConsoleProcs          = 64
)

var (
	kernel32             = syscall.NewLazyDLL("kernel32.dll")
	procAttachConsole    = kernel32.NewProc("AttachConsole")
	procConsoleProcesses = kernel32.NewProc("GetConsoleProcessList")
	procFreeConsole      = kernel32.NewProc("FreeConsole")
	procSetConsoleMode   = kernel32.NewProc("SetConsoleMode")
	procSetStdHandle     = kernel32.NewProc("SetStdHandle")
)

func Attach() bool {
	stdin := standardHandle(syscall.STD_INPUT_HANDLE)
	stdout := standardHandle(syscall.STD_OUTPUT_HANDLE)
	stderr := standardHandle(syscall.STD_ERROR_HANDLE)

	processCount := consoleProcessCount()
	if processCount == 0 {
		r, _, _ := procAttachConsole.Call(attachParentProcess)
		if r == 0 {
			return false
		}
		processCount = consoleProcessCount()
		if processCount == 0 {
			return false
		}
	}

	os.Stdin = resolveStandardFile(syscall.STD_INPUT_HANDLE, stdin, os.Stdin, "CONIN$")
	os.Stdout = resolveStandardFile(syscall.STD_OUTPUT_HANDLE, stdout, os.Stdout, "CONOUT$")
	os.Stderr = resolveStandardFile(syscall.STD_ERROR_HANDLE, stderr, os.Stderr, "CONOUT$")
	normalizeConsoleOutput(os.Stdout)
	normalizeConsoleOutput(os.Stderr)

	if processCount != 1 {
		return true
	}
	silenceConsoleOutput()
	_, _, _ = procFreeConsole.Call()
	return false
}

func standardHandle(id int) syscall.Handle {
	handle, err := syscall.GetStdHandle(id)
	if err != nil || !validHandle(handle) {
		return syscall.InvalidHandle
	}
	return handle
}

func validHandle(handle syscall.Handle) bool {
	return handle != 0 && handle != syscall.InvalidHandle
}

func resolveStandardFile(id int, inherited syscall.Handle, original *os.File, consoleName string) *os.File {
	if validHandle(inherited) {
		_, _, _ = procSetStdHandle.Call(uintptr(id), uintptr(inherited))
		return original
	}
	handle := standardHandle(id)
	if !validHandle(handle) {
		return original
	}
	return os.NewFile(uintptr(handle), consoleName)
}

func normalizeConsoleOutput(file *os.File) {
	handle := consoleHandle(file)
	if !validHandle(handle) {
		return
	}
	var mode uint32
	if err := syscall.GetConsoleMode(handle, &mode); err != nil {
		return
	}
	normalized := mode &^ disableNewlineAutoReturn
	if normalized != mode {
		_, _, _ = procSetConsoleMode.Call(uintptr(handle), uintptr(normalized))
	}
}

func consoleHandle(file *os.File) syscall.Handle {
	if file == nil {
		return syscall.InvalidHandle
	}
	handle := syscall.Handle(file.Fd())
	if !validHandle(handle) {
		return syscall.InvalidHandle
	}
	return handle
}

func isConsoleFile(file *os.File) bool {
	handle := consoleHandle(file)
	if !validHandle(handle) {
		return false
	}
	var mode uint32
	return syscall.GetConsoleMode(handle, &mode) == nil
}

func silenceConsoleOutput() {
	if isConsoleFile(os.Stdout) {
		if file, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0); err == nil {
			os.Stdout = file
		}
	}
	if isConsoleFile(os.Stderr) {
		if file, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0); err == nil {
			os.Stderr = file
		}
	}
}

func consoleProcessCount() int {
	var pids [maxConsoleProcs]uint32
	count, _, _ := procConsoleProcesses.Call(uintptr(unsafe.Pointer(&pids[0])), uintptr(len(pids)))
	return int(count)
}
