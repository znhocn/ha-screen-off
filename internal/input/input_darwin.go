//go:build darwin

package input

import (
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"
)

// macOS idle detection via the IOHIDSystem HIDIdleTime, read from ioreg.
// No cgo / CoreGraphics needed, so the binary cross-compiles cleanly.
// `ioreg` is slow (~tens of ms), so results are cached with a 1s TTL - the
// watchdog probes every few hundred ms while the screen is off.

type macosWatcher struct {
	mu       sync.Mutex
	lastAt   time.Time
	lastIdle time.Duration
}

// newPlatform is the macOS entry point.
func newPlatform() (Watcher, error) {
	if _, err := exec.LookPath("ioreg"); err != nil {
		return nil, fmt.Errorf("ioreg not found")
	}
	platformMethod = "macos-ioreg-HIDIdleTime"
	return &macosWatcher{}, nil
}

func (w *macosWatcher) IdleSince() time.Duration {
	w.mu.Lock()
	defer w.mu.Unlock()
	if time.Since(w.lastAt) < time.Second {
		return w.lastIdle
	}
	w.lastAt = time.Now()
	out, err := exec.Command("ioreg", "-c", "IOHIDSystem").Output()
	if err != nil {
		return w.lastIdle // transient failure: keep the previous reading
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
		w.lastIdle = time.Duration(ns) * time.Nanosecond
		return w.lastIdle
	}
	return w.lastIdle
}

func (w *macosWatcher) Close() {}
