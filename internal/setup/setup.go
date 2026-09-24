// Package setup provides the interactive `scroff setup` flow that
// generates a config file under ~/.config/scroff/.
package setup

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"scroff/internal/config"
)

// ConfigDir returns the directory where the config file lives, creating it if
// needed. Always $HOME/.config/scroff (works on Linux, macOS, Windows).
func ConfigDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("cannot resolve home directory: %w", err)
	}
	dir := filepath.Join(home, ".config", "scroff")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("create config dir %q: %w", dir, err)
	}
	return dir, nil
}

// ConfigPath returns the absolute config file path.
func ConfigPath() (string, error) {
	dir, err := ConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "config.json"), nil
}

// Interactive walks the user through the config values (defaults are shown and
// accepted with an empty answer), then writes the file. Returns the path that
// was written.
func Interactive(r io.Reader, w io.Writer) (string, error) {
	p := &prompter{r: bufio.NewReader(r), w: w}

	cfg := config.Default()
	cfg.HA.URL = p.text("Home Assistant base URL", cfg.HA.URL, false)
	cfg.HA.Token = p.required("Long-lived access token (Home Assistant > user > Security)", "token")
	cfg.HA.EntityID = p.text("Entity id to watch (e.g. input_boolean.screen_power)", cfg.HA.EntityID, false)
	cfg.HA.PollIntervalS = p.intQ("Poll Interval (seconds)", cfg.HA.PollIntervalS)
	cfg.HA.TimeoutS = p.intQ("HTTP timeout (seconds)", cfg.HA.TimeoutS)
	cfg.HA.InsecureTLS = p.boolQ("Skip TLS verification? (yes if https + self-signed cert)", cfg.HA.InsecureTLS)

	fmt.Fprintf(w, "\n-- screen settings --\n")

	if err := p.optionalBackend(&cfg); err != nil {
		return "", err
	}
	cfg.Screen.ForceOffIntervalS = p.intQ("Re-assert screen-off interval (seconds)", cfg.Screen.ForceOffIntervalS)
	cfg.Screen.ActiveThresholdMs = p.intQ("Idle time below which the user is considered active (ms)", cfg.Screen.ActiveThresholdMs)
	cfg.Screen.WatchIntervalMs = p.intQ("Watchdog resolution (ms)", cfg.Screen.WatchIntervalMs)
	cfg.Log.Level = p.text("Log level (debug/info/warn/error)", cfg.Log.Level, false)

	path, err := ConfigPath()
	if err != nil {
		return "", err
	}
	if _, statErr := os.Stat(path); statErr == nil {
		overwrite := p.boolQ(fmt.Sprintf("%s already exists - overwrite?", path), true)
		if !overwrite {
			return "", fmt.Errorf("aborted, existing config kept at %s", path)
		}
	} else if !os.IsNotExist(statErr) {
		return "", statErr
	}

	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return "", err
	}
	data = append(data, '\n')
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return "", fmt.Errorf("write config: %w", err)
	}
	fmt.Fprintf(w, "\nConfig written to %s\n\n", path)
	fmt.Fprintf(w, "Run it now:\n  scroff serve\n\n")
	return path, nil
}

// prompter reads interactive answers from a reader.
type prompter struct {
	r *bufio.Reader
	w io.Writer
}

func (p *prompter) label(label string) {
	fmt.Fprintf(p.w, "%s: ", label)
}

// text reads a line; an empty answer yields def. secret is not echoed back.
func (p *prompter) text(label, def string, secret bool) string {
	fmt.Fprintf(p.w, "%s [%s]: ", label, displayDefault(def))
	line, _ := p.r.ReadString('\n')
	line = strings.TrimSpace(line)
	if line == "" {
		return def
	}
	return line
}

// required reads a non-empty value (no default).
func (p *prompter) required(label, what string) string {
	for {
		fmt.Fprintf(p.w, "%s: ", label)
		line, _ := p.r.ReadString('\n')
		line = strings.TrimSpace(line)
		if line != "" {
			return line
		}
		fmt.Fprintf(p.w, "%s must not be empty, try again.\n", what)
	}
}

func (p *prompter) intQ(label string, def int) int {
	s := p.text(label, strconv.Itoa(def), false)
	v, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil {
		return def
	}
	return v
}

func (p *prompter) boolQ(label string, def bool) bool {
	hint := "n"
	if def {
		hint = "y"
	}
	s := p.text(label+" (y/n)", hint, false)
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "y", "yes", "true", "1":
		return true
	case "n", "no", "false", "0":
		return false
	}
	return def
}

// optionalBackend asks for the Linux backend only on Linux.
func (p *prompter) optionalBackend(cfg *config.Config) error {
	if runtime.GOOS == "windows" {
		return nil
	}
	cfg.Screen.LinuxBackend = p.text("Linux screen backend (auto/x11/wayland/ddc)", cfg.Screen.LinuxBackend, false)
	return nil
}

func displayDefault(def string) string {
	if def == "" {
		return "required"
	}
	return def
}
