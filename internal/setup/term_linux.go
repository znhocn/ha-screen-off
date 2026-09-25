//go:build linux

package setup

import (
	"syscall"
	"unsafe"
)

// termios mirrors struct termios on Linux (tcflag_t is unsigned int on every
// arch, cc_t c_cc has 19 entries).
type termios struct {
	Iflag, Oflag, Cflag, Lflag uint32
	Line                       byte
	Cc                         [19]byte
	Ispeed, Ospeed             uint32
}

// hideStdinEcho disables terminal echo on stdin and returns a function that
// restores the previous state. Errors (e.g. stdin is not a terminal) mean echo
// control is unnecessary; callers proceed without it.
func hideStdinEcho() (func(), error) {
	fd := int(syscall.Stdin)
	var t termios
	if _, _, e := syscall.Syscall(syscall.SYS_IOCTL, uintptr(fd), uintptr(syscall.TCGETS), uintptr(unsafe.Pointer(&t))); e != 0 {
		return nil, e
	}
	orig := t
	t.Lflag &^= 0x0008 // ECHO
	if _, _, e := syscall.Syscall(syscall.SYS_IOCTL, uintptr(fd), uintptr(syscall.TCSETS), uintptr(unsafe.Pointer(&t))); e != 0 {
		return nil, e
	}
	return func() {
		syscall.Syscall(syscall.SYS_IOCTL, uintptr(fd), uintptr(syscall.TCSETS), uintptr(unsafe.Pointer(&orig)))
	}, nil
}
