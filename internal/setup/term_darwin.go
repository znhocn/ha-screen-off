//go:build darwin

package setup

import (
	"syscall"
	"unsafe"
)

// Darwin ioctl request codes for termios (not exported by the syscall package).
const (
	tcgets = 0x40487413 // TIOCGETA
	tcsets = 0x40287419 // TIOCSETA
)

// termios mirrors struct termios on Darwin (NCCS = 20).
type termios struct {
	Iflag, Oflag, Cflag, Lflag uint32
	Cc                         [20]byte
	Ispeed, Ospeed             uint32
}

// hideStdinEcho disables terminal echo on stdin and returns a function that
// restores the previous state. Errors (e.g. stdin is not a terminal) mean echo
// control is unnecessary; callers proceed without it.
func hideStdinEcho() (func(), error) {
	fd := int(syscall.Stdin)
	var t termios
	if _, _, e := syscall.Syscall(syscall.SYS_IOCTL, uintptr(fd), uintptr(tcgets), uintptr(unsafe.Pointer(&t))); e != 0 {
		return nil, e
	}
	orig := t
	t.Lflag &^= 0x0008 // ECHO
	if _, _, e := syscall.Syscall(syscall.SYS_IOCTL, uintptr(fd), uintptr(tcsets), uintptr(unsafe.Pointer(&t))); e != 0 {
		return nil, e
	}
	return func() {
		syscall.Syscall(syscall.SYS_IOCTL, uintptr(fd), uintptr(tcsets), uintptr(unsafe.Pointer(&orig)))
	}, nil
}
