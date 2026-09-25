//go:build windows

package setup

import (
	"syscall"
	"unsafe"
)

const (
	stdInputHandle         = ^uintptr(10) // -10, STD_INPUT_HANDLE
	enableEchoInput uint32 = 0x0004
)

var (
	modKernel32        = syscall.NewLazyDLL("kernel32.dll")
	procGetStdHandle   = modKernel32.NewProc("GetStdHandle")
	procGetConsoleMode = modKernel32.NewProc("GetConsoleMode")
	procSetConsoleMode = modKernel32.NewProc("SetConsoleMode")
)

// hideStdinEcho disables console input echo on stdin and returns a function
// that restores the previous state. Errors (e.g. stdin is redirected) mean
// echo control is unnecessary; callers proceed without it.
func hideStdinEcho() (func(), error) {
	h, _, _ := procGetStdHandle.Call(stdInputHandle)
	if h == 0 || h == uintptr(^uintptr(0)) { // NULL or INVALID_HANDLE_VALUE
		return nil, syscall.EINVAL
	}
	var mode uint32
	if r, _, _ := procGetConsoleMode.Call(h, uintptr(unsafe.Pointer(&mode))); r == 0 {
		return nil, syscall.EINVAL
	}
	orig := mode
	mode &^= enableEchoInput
	if r, _, _ := procSetConsoleMode.Call(h, uintptr(mode)); r == 0 {
		return nil, syscall.EINVAL
	}
	return func() {
		procSetConsoleMode.Call(h, uintptr(orig))
	}, nil
}
