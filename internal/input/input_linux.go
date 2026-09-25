//go:build linux

package input

import (
	"encoding/binary"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

// Linux idle detection:
//
// Primary: poll /dev/input/event* (evdev) for EV_KEY / EV_REL / EV_ABS events.
// This works on both X11 and Wayland with no display-server dependency.
//
// Fallback: if no input devices are readable (permissions, weird setups) and an
// X display is present, use the `xprintidle` helper if installed.

const (
	evSyn = 0 // separator / sync
	evKey = 1
	evRel = 2
	evAbs = 3
)

// inputEvent is struct input_event from linux/input.h (24 bytes on 64-bit).
type inputEvent struct {
	Sec   int64
	Usec  int64
	Type  uint16
	Code  uint16
	Value int32
}

type evdevWatcher struct {
	last   atomic.Int64  // unix nano timestamp of the most recent input event
	closed atomic.Bool   // guards resources against a second Close
	done   chan struct{} // closed on Close; releases the device rescan loop

	mu     sync.Mutex
	fds    map[int]struct{} // open devices; each fd is closed exactly once
	pathOf map[int]string   // fd -> device path (for hotplug dedupe)
}

// newPlatform is the Linux entry point.
func newPlatform() (Watcher, error) {
	w, err := newEvdev()
	if err == nil {
		platformMethod = "linux-evdev"
		return w, nil
	}
	if xw, xErr := newXprintidle(); xErr == nil {
		platformMethod = "linux-xprintidle"
		return xw, nil
	}
	return nil, err
}

// newEvdev opens every accessible /dev/input/event* device and starts one
// blocking-read goroutine per device. A background loop keeps watching for
// devices that appear later (hotplug).
func newEvdev() (Watcher, error) {
	devs, err := filepath.Glob("/dev/input/event*")
	if err != nil || len(devs) == 0 {
		return nil, fmt.Errorf("no /dev/input/event* devices found")
	}
	w := &evdevWatcher{
		done:   make(chan struct{}),
		fds:    make(map[int]struct{}),
		pathOf: make(map[int]string),
	}
	for _, dev := range devs {
		w.openDevice(dev)
	}
	w.mu.Lock()
	opened := len(w.fds)
	w.mu.Unlock()
	if opened == 0 {
		return nil, fmt.Errorf("cannot open /dev/input/event* (permissions?) - run as a member of the input group or as root, or install xprintidle")
	}
	w.last.Store(time.Now().UnixNano())
	go w.rescanLoop()
	return w, nil
}

// openDevice opens dev unless it is already open (dedupe on path). Safe to
// call from newEvdev and the rescan loop. If the watcher is being shut down
// concurrently, the freshly opened fd is closed again instead of added.
func (w *evdevWatcher) openDevice(dev string) {
	fd, oerr := syscall.Open(dev, syscall.O_RDONLY|syscall.O_CLOEXEC, 0)
	if oerr != nil {
		return // EACCES / not ours; try the rest later
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed.Load() {
		// Close() drained the tables and may already have returned; do not
		// leak this fd or a read goroutine past shutdown.
		_ = syscall.Close(fd)
		return
	}
	for _, p := range w.pathOf {
		if p == dev {
			_ = syscall.Close(fd)
			return
		}
	}
	w.fds[fd] = struct{}{}
	w.pathOf[fd] = dev
	go w.readLoop(fd)
}

// rescanLoop periodically re-globs /dev/input/event* so devices plugged in
// while the watchdog runs (USB keyboards, mice) are picked up. openDevice
// dedupes, so already-open paths are simply skipped.
func (w *evdevWatcher) rescanLoop() {
	t := time.NewTicker(2 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-w.done:
			return
		case <-t.C:
			devs, err := filepath.Glob("/dev/input/event*")
			if err != nil {
				continue
			}
			for _, dev := range devs {
				w.openDevice(dev)
			}
		}
	}
}

// readLoop blocks reading HID events from one device and stamps activity time.
// Each fd is closed exactly once (here, or by Close) - guarded by the fds map.
func (w *evdevWatcher) readLoop(fd int) {
	defer w.removeFd(fd)
	buf := make([]byte, 64)
	for {
		n, err := syscall.Read(fd, buf)
		if err != nil {
			return // device unplugged or unreadable - stop watching this one
		}
		if parseIsActivity(buf[:n]) {
			w.last.Store(time.Now().UnixNano())
		}
	}
}

// removeFd closes fd exactly once. Either readLoop calls it (device unplugged)
// or Close does (shutdown); the map membership makes the second call a no-op,
// so the same descriptor integer can never be closed twice.
func (w *evdevWatcher) removeFd(fd int) {
	w.mu.Lock()
	if _, ok := w.fds[fd]; ok {
		delete(w.fds, fd)
		delete(w.pathOf, fd)
		syscall.Close(fd)
	}
	w.mu.Unlock()
}

// parseIsActivity returns true if the raw bytes contain a meaningful HID input
// event (key press/release, mouse movement/scroll, absolute position).
func parseIsActivity(b []byte) bool {
	for len(b) >= 24 {
		ev := inputEvent{
			Sec:   int64(binary.LittleEndian.Uint64(b[0:8])),
			Usec:  int64(binary.LittleEndian.Uint64(b[8:16])),
			Type:  binary.LittleEndian.Uint16(b[16:18]),
			Code:  binary.LittleEndian.Uint16(b[18:20]),
			Value: int32(binary.LittleEndian.Uint32(b[20:24])),
		}
		if ev.Type == evKey || ev.Type == evRel || ev.Type == evAbs {
			return true
		}
		b = b[24:]
	}
	return false
}

func (w *evdevWatcher) IdleSince() time.Duration {
	last := time.Unix(0, w.last.Load())
	d := time.Since(last)
	if d < 0 {
		return 0
	}
	return d
}

func (w *evdevWatcher) Close() {
	if !w.closed.Swap(true) {
		close(w.done) // wake and exit the rescan loop
		w.mu.Lock()
		for fd := range w.fds {
			delete(w.fds, fd)
			delete(w.pathOf, fd)
			syscall.Close(fd)
		}
		w.mu.Unlock()
	}
}

// ---------------------------------------------------------------------------
// xprintidle fallback (X11 only).
// ---------------------------------------------------------------------------

// xprintidleWatcher shells out to `xprintidle`. The watchdog probes idle every
// few hundred ms while the screen is off; a cached value (1s TTL) keeps that
// from spawning a subprocess 4x/second.
type xprintidleWatcher struct {
	mu       sync.Mutex
	lastAt   time.Time
	lastIdle time.Duration
}

func newXprintidle() (Watcher, error) {
	if os.Getenv("DISPLAY") == "" {
		return nil, fmt.Errorf("no DISPLAY")
	}
	if _, err := exec.LookPath("xprintidle"); err != nil {
		return nil, err
	}
	return &xprintidleWatcher{}, nil
}

func (x *xprintidleWatcher) IdleSince() time.Duration {
	x.mu.Lock()
	defer x.mu.Unlock()
	if time.Since(x.lastAt) < time.Second {
		return x.lastIdle
	}
	x.lastAt = time.Now()
	out, err := exec.Command("xprintidle").Output()
	if err != nil {
		return x.lastIdle // transient failure: keep the previous reading
	}
	ms, err := strconv.ParseInt(strings.TrimSpace(string(out)), 10, 64)
	if err != nil {
		return x.lastIdle
	}
	x.lastIdle = time.Duration(ms) * time.Millisecond
	return x.lastIdle
}

func (x *xprintidleWatcher) Close() {}
