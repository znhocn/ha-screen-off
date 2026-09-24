//go:build linux

package screen

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// Linux display control. Three backends are supported, chosen at runtime:
//
//   - x11:     DPMS via `xset dpms force off|on` (X.Org / XWayland sessions).
//   - wayland: per-output power via `wlopm` (wlroots-based compositors).
//   - ddc:     DDC/CI VCP 0xD6 via `ddcutil` (works on both X11 and Wayland,
//     but is opt-in because it changes the monitors' own power).
//
// The dispatch/query logic lives here; platform kernels other than Linux use
// their own files.
//
// wlopm note: `--off`/`--on` require an output name and do nothing on their
// own, so all outputs must be addressed explicitly with "*".

type linuxBackend string

const (
	backendX11     linuxBackend = "x11"
	backendWayland linuxBackend = "wayland"
	backendDDC     linuxBackend = "ddc"
)

type linuxController struct {
	backend linuxBackend
}

// newPlatform is the Linux entry point.
func newPlatform(want string) (Controller, error) {
	backend, err := resolveBackend(want)
	if err != nil {
		return nil, err
	}
	c := &linuxController{backend: backend}
	if err := c.Available(); err != nil {
		return nil, err
	}
	return c, nil
}

// resolveBackend maps the configured/auto value to a concrete backend.
func resolveBackend(want string) (linuxBackend, error) {
	switch want {
	case "x11":
		return backendX11, nil
	case "wayland":
		return backendWayland, nil
	case "ddc":
		return backendDDC, nil
	case "auto", "":
		// Trust XDG_SESSION_TYPE first. Note: GNOME/Wayland sets DISPLAY=:1 for
		// XWayland too, but xset only affects the XWayland screen, not the real
		// outputs - so a wayland session always means the wayland backend.
		switch os.Getenv("XDG_SESSION_TYPE") {
		case "wayland":
			return backendWayland, nil
		case "x11":
			return backendX11, nil
		}
		if os.Getenv("WAYLAND_DISPLAY") != "" {
			return backendWayland, nil
		}
		if os.Getenv("DISPLAY") != "" {
			return backendX11, nil
		}
		return "", fmt.Errorf("cannot auto-detect the display server (no XDG_SESSION_TYPE/DISPLAY/WAYLAND_DISPLAY) - set screen.linux_backend to x11 or wayland explicitly")
	default:
		return "", fmt.Errorf("unknown linux_backend %q (want auto|x11|wayland|ddc)", want)
	}
}

func (l *linuxController) Backend() string { return "linux-" + string(l.backend) }

func (l *linuxController) Available() error {
	switch l.backend {
	case backendX11:
		return lookup("xset")
	case backendWayland:
		return lookup("wlopm")
	case backendDDC:
		return lookup("ddcutil")
	}
	return nil
}

func (l *linuxController) Off() error {
	switch l.backend {
	case backendX11:
		return run("xset", "dpms", "force", "off")
	case backendWayland:
		return run("wlopm", "--off", "*")
	case backendDDC:
		return run("ddcutil", "setvcp", "0xD6", "0x04")
	}
	return fmt.Errorf("unsupported backend %q", l.backend)
}

func (l *linuxController) On() error {
	switch l.backend {
	case backendX11:
		return run("xset", "dpms", "force", "on")
	case backendWayland:
		return run("wlopm", "--on", "*")
	case backendDDC:
		return run("ddcutil", "setvcp", "0xD6", "0x01")
	}
	return fmt.Errorf("unsupported backend %q", l.backend)
}

// IsOff reports the current display power state.
//
//	x11: parse `xset q` -> "Monitor is On/Off".
//	wayland: parse `wlopm` -> "(off)"/"(on)".
func (l *linuxController) IsOff() (bool, error) {
	switch l.backend {
	case backendX11:
		out, err := exec.Command("xset", "q").Output()
		if err != nil {
			return false, fmt.Errorf("xset q: %w", err)
		}
		// "Monitor is Off" present means the session display is asleep.
		hasOn := bytes.Contains(out, []byte("Monitor is On"))
		hasOff := bytes.Contains(out, []byte("Monitor is Off"))
		if hasOff {
			return true, nil
		}
		if hasOn {
			return false, nil
		}
		return false, nil
	case backendWayland:
		out, err := exec.Command("wlopm").Output()
		if err != nil {
			return false, fmt.Errorf("wlopm: %w", err)
		}
		lines := strings.Split(strings.TrimSpace(string(out)), "\n")
		off, on := 0, 0
		for _, ln := range lines {
			switch {
			case strings.Contains(ln, "(off)"):
				off++
			case strings.Contains(ln, "(on)"):
				on++
			}
		}
		if off > 0 && on == 0 {
			return true, nil
		}
		return false, nil
	case backendDDC:
		// ddcutil lacks an obvious "current power" probe; treat as unknown and
		// let the watchdog re-assert Off() on an interval.
		return false, ErrNoQuery
	}
	return false, nil
}

func run(name string, args ...string) error {
	if err := lookup(name); err != nil {
		return err
	}
	cmd := exec.Command(name, args...)
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s %s: %w: %s", name, strings.Join(args, " "), err, strings.TrimSpace(buf.String()))
	}
	return nil
}
