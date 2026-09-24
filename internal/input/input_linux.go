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
	last atomic.Int64 // unix nano timestamp of the most recent input event
	stop chan struct{}
	fds  []int
	once sync.Once
}

// newPlatform is the Linux entry point.
func newPlatform() (Watcher, error) {
	w, err := newEvdev()
	if err == nil {
		return w, nil
	}
	if xw, xErr := newXprintidle(); xErr == nil {
		return xw, nil
	}
	return nil, err
}

// newEvdev opens every accessible /dev/input/event* device and starts one
// blocking-read goroutine per device.
func newEvdev() (Watcher, error) {
	devs, err := filepath.Glob("/dev/input/event*")
	if err != nil || len(devs) == 0 {
		return nil, fmt.Errorf("no /dev/input/event* devices found")
	}
	w := &evdevWatcher{
		stop: make(chan struct{}),
	}
	for _, dev := range devs {
		fd, oerr := syscall.Open(dev, syscall.O_RDONLY|syscall.O_CLOEXEC, 0)
		if oerr != nil {
			continue // EACCES / not ours; try the rest
		}
		w.fds = append(w.fds, fd)
		go w.readLoop(fd)
	}
	if len(w.fds) == 0 {
		return nil, fmt.Errorf("cannot open /dev/input/event* (permissions?) - run as a member of the input group or as root, or install xprintidle")
	}
	w.last.Store(time.Now().UnixNano())
	return w, nil
}

// readLoop blocks reading HID events from one device and stamps activity time.
func (w *evdevWatcher) readLoop(fd int) {
	defer syscall.Close(fd)
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
	w.once.Do(func() { close(w.stop) })
	for _, fd := range w.fds {
		syscall.Close(fd)
	}
}

// ---------------------------------------------------------------------------
// xprintidle fallback (X11 only).
// ---------------------------------------------------------------------------

type xprintidleWatcher struct{}

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
	out, err := exec.Command("xprintidle").Output()
	if err != nil {
		return 0
	}
	ms, err := strconv.ParseInt(strings.TrimSpace(string(out)), 10, 64)
	if err != nil {
		return 0
	}
	return time.Duration(ms) * time.Millisecond
}

func (x *xprintidleWatcher) Close() {}

func method() string { return "linux-evdev" }
