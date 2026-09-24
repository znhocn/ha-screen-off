// Package input detects mouse/keyboard (and other HID) activity so the
// controller can auto-wake the display when the user interacts again.
//
// Each platform exposes an Idle probe in the platform-specific files:
//
//	Windows: GetLastInputInfo
//	macOS:   ioreg HIDIdleTime
//	Linux:   /dev/input/event* (evdev) polling - works on both X11 and Wayland,
//	         no display server dependency, tracked by a background goroutine.
package input

import (
	"fmt"
	"runtime"
	"time"
)

// Watcher reports how long the machine has been idle (free of user input).
type Watcher interface {
	// IdleSince returns the duration since the last detected input event.
	// A zero-ish duration means the user is currently interacting.
	IdleSince() time.Duration
	// Close releases any underlying resources (goroutines, fds).
	Close()
}

// New builds a Watcher for this operating system.
func New() (Watcher, error) {
	return newPlatform()
}

// NewNoop returns a watcher that never reports activity. It is used when the
// real idle detector cannot be initialised (e.g. no /dev/input permissions):
// the watchdog then keeps the screen off and relies on Home Assistant to wake
// it, instead of dying at startup.
func NewNoop() Watcher {
	return noopWatcher{}
}

// noopWatcher always reports a huge idle time.
type noopWatcher struct{}

func (noopWatcher) IdleSince() time.Duration { return time.Hour }

func (noopWatcher) Close() {}

// Method reports the idle-detection technique in use on this platform, for
// diagnostics and logs.
func Method() string { return method() }

// unsupported is used on platforms where idle detection is not implemented.
func unsupported() (Watcher, error) {
	return nil, fmt.Errorf("idle detection is not supported on %s", runtime.GOOS)
}

// min is a tiny helper to avoid depending on slices/min for older Go readers.
func min(a, b time.Duration) time.Duration {
	if a < b {
		return a
	}
	return b
}
