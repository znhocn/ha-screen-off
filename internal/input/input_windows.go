//go:build windows

package input

import (
	"fmt"
	"syscall"
	"time"
	"unsafe"
)

// Windows idle detection via GetLastInputInfo.

var (
	user32        = syscall.NewLazyDLL("user32.dll")
	procGII       = user32.NewProc("GetLastInputInfo")
	kernel32      = syscall.NewLazyDLL("kernel32.dll")
	procTickCount = kernel32.NewProc("GetTickCount")
)

type lastInputInfo struct {
	cbSize uint32
	dwTime uint32 // tick count of last input
}

type windowsWatcher struct{}

// newPlatform is the Windows entry point.
func newPlatform() (Watcher, error) {
	if err := user32.Load(); err != nil {
		return nil, fmt.Errorf("load user32.dll: %w", err)
	}
	return &windowsWatcher{}, nil
}

// getTick returns milliseconds since boot (wraps at ~49 days, fine here).
func getTick() uint32 {
	r, _, _ := procTickCount.Call()
	return uint32(r)
}

func (w *windowsWatcher) IdleSince() time.Duration {
	info := lastInputInfo{cbSize: uint32(unsafe.Sizeof(lastInputInfo{}))}
	r, _, _ := procGII.Call(uintptr(unsafe.Pointer(&info)))
	if uintptr(r) == 0 {
		return 0
	}
	now := getTick()
	if info.dwTime > now {
		return 0 // tick wrapped; treat as active to be safe
	}
	return time.Duration(now-info.dwTime) * time.Millisecond
}

func (w *windowsWatcher) Close() {}

func method() string { return "windows-GetLastInputInfo" }
