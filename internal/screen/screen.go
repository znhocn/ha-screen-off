// Package screen controls display power (on/off) in a cross-platform way.
//
// Implementations are chosen per operating system at build time; on Linux the
// backend (x11/wayland/ddc) can also be chosen at runtime from configuration.
//
// This package only uses the Go standard library so that the resulting binary
// is fully self-contained and cross-compilable from any host.
package screen

import (
	"errors"
	"fmt"
	"os/exec"
	"runtime"
)

// ErrNoQuery is returned by Controller.IsOff when the platform or backend
// cannot report the current display power state. Callers should fall back to
// re-asserting Off() on an interval.
var ErrNoQuery = errors.New("screen: state query not supported on this platform")

// Controller abstracts turning the screen on and off.
type Controller interface {
	// Backend reports the active implementation (e.g. "windows", "macos-pmset",
	// "linux-x11", "linux-wayland").
	Backend() string
	// Available returns nil if the backend's required tools are present.
	Available() error
	// Off turns the display(s) off.
	Off() error
	// On turns the display(s) on.
	On() error
	// IsOff reports whether the display is currently powered off.
	// Returns (false, nil) when the platform cannot query the state; callers
	// should fall back to re-asserting Off() on an interval.
	IsOff() (bool, error)
}

// New builds a Controller for this operating system.
// linuxBackend is one of auto|x11|wayland|ddc (ignored on other OSes).
func New(linuxBackend string) (Controller, error) {
	return newPlatform(linuxBackend)
}

// lookup runs exec.LookPath and returns a friendlier error when missing.
func lookup(name string) error {
	if _, err := exec.LookPath(name); err != nil {
		return fmt.Errorf("%s not found in PATH (required by scroff)", name)
	}
	return nil
}

// plat is a helper for reporting which OS we compiled for (debug/status output).
func plat() string {
	return runtime.GOOS
}
