//go:build darwin

package input

import (
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// macOS idle detection via the IOHIDSystem HIDIdleTime, read from ioreg.
// No cgo / CoreGraphics needed, so the binary cross-compiles cleanly.

type macosWatcher struct{}

// newPlatform is the macOS entry point.
func newPlatform() (Watcher, error) {
	if _, err := exec.LookPath("ioreg"); err != nil {
		return nil, fmt.Errorf("ioreg not found")
	}
	return &macosWatcher{}, nil
}

func (w *macosWatcher) IdleSince() time.Duration {
	out, err := exec.Command("ioreg", "-c", "IOHIDSystem").Output()
	if err != nil {
		return 0 // transient failure: report "active" so we never lock someone out
	}
	// Line looks like: "...  "HIDIdleTime" = 1234567890"
	for _, line := range strings.Split(string(out), "\n") {
		if !strings.Contains(line, "HIDIdleTime") {
			continue
		}
		parts := strings.Split(line, "=")
		if len(parts) != 2 {
			continue
		}
		ns, err := strconv.ParseInt(strings.TrimSpace(parts[1]), 10, 64)
		if err != nil {
			continue
		}
		return time.Duration(ns) * time.Nanosecond
	}
	return 0
}

func (w *macosWatcher) Close() {}

func method() string { return "macos-ioreg-HIDIdleTime" }
