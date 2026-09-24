//go:build darwin

package screen

import (
	"bytes"
	"fmt"
	"os/exec"
	"regexp"
	"strings"
)

// macOS display control via standard Apple utilities: pmset, caffeinate and
// ioreg. All three ship with macOS so no extra installs are needed.

var iorePowerRegexp = regexp.MustCompile(`"DevicePowerState"=(\d+)`)

type macosController struct{}

// newPlatform is the macOS entry point.
func newPlatform(_ string) (Controller, error) {
	c := &macosController{}
	if err := c.Available(); err != nil {
		return nil, err
	}
	return c, nil
}

func (m *macosController) Backend() string { return "macos" }

func (m *macosController) Available() error {
	for _, name := range []string{"pmset", "caffeinate", "ioreg"} {
		if err := lookup(name); err != nil {
			return err
		}
	}
	return nil
}

// Off puts the displays immediately to sleep.
func (m *macosController) Off() error {
	out, err := exec.Command("pmset", "displaysleepnow").CombinedOutput()
	if err != nil {
		return fmt.Errorf("pmset displaysleepnow: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// On wakes the displays by asserting activity for 1 second.
func (m *macosController) On() error {
	if err := exec.Command("caffeinate", "-u", "-t", "1").Run(); err != nil {
		return fmt.Errorf("caffeinate -u: %w", err)
	}
	return nil
}

// IsOff inspects the IODisplayWrangler power state via ioreg.
func (m *macosController) IsOff() (bool, error) {
	out, err := exec.Command("ioreg", "-n", "IODisplayWrangler", "-r", "-d", "1").Output()
	if err != nil {
		return false, fmt.Errorf("ioreg: %w", err)
	}
	// IODisplayWrangler reports a DevicePowerState per display; 0 = off.
	// Like the wayland backend, report "off" only when every display is off.
	match := iorePowerRegexp.FindAllSubmatch(bytes.TrimSpace(out), -1)
	if len(match) == 0 {
		return false, nil
	}
	off := true
	for _, m := range match {
		var state int
		_, _ = fmt.Sscanf(string(m[1]), "%d", &state)
		if state != 0 {
			off = false
			break
		}
	}
	return off, nil
}
