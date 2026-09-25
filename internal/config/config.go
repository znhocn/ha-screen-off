// Package config handles loading and merging the scroff (HA Screen Off)
// configuration.
package config

import (
	"encoding/json"
	"os"
)

// Config is the full runtime configuration. A zero value falls back to defaults.
type Config struct {
	HA     HAConfig     `json:"ha"`
	Screen ScreenConfig `json:"screen"`
	Log    LogConfig    `json:"log"`
}

// HAConfig holds the Home Assistant connection settings.
type HAConfig struct {
	// URL is the base URL of the Home Assistant instance, e.g. http://192.168.1.10:8123
	URL string `json:"url"`
	// Token is a long-lived access token created in Home Assistant.
	Token string `json:"token"`
	// EntityID is the binary entity that controls the screen, e.g. input_boolean.screen_power.
	EntityID string `json:"entity_id"`
	// PollIntervalS how often to poll the entity state (seconds).
	PollIntervalS int `json:"poll_interval_s"`
	// TimeoutS HTTP timeout for HA requests (seconds).
	TimeoutS int `json:"timeout_s"`
	// InsecureTLS skips TLS certificate verification. Needed when HA is served
	// over https with a self-signed certificate.
	InsecureTLS bool `json:"insecure_tls"`
}

// ScreenConfig tunes the screen behaviour and the input watchdog.
type ScreenConfig struct {
	// LinuxBackend selects the screen control backend on Linux:
	// auto | x11 | wayland | ddc. Auto-detects from the session type.
	LinuxBackend string `json:"linux_backend"`
	// ForceOffIntervalS how often to re-assert the screen is off while waiting
	// for user input. Helps fight unwanted wakes from notifications/background apps.
	ForceOffIntervalS int `json:"force_off_interval_s"`
	// ActiveThresholdMs: if idle time is below this, consider the user active and wake.
	ActiveThresholdMs int `json:"active_threshold_ms"`
	// WatchIntervalMs: watchdog resolution while in screen-off mode.
	WatchIntervalMs int `json:"watch_interval_ms"`
}

// LogConfig tunes logging.
type LogConfig struct {
	// Level is one of debug, info, warn, error.
	Level string `json:"level"`
	// Verbose enables debug logging (equivalent to Level=debug).
	Verbose bool `json:"verbose"`
}

// Upper bounds for time-derived values. They keep main.go's
// time.Duration(value)*unit multiplications from overflowing into negative
// durations (which would panic inside time.NewTicker), since only values that
// are absurdly large could ever cause that.
const (
	maxPollIntervalS     = 3600
	maxTimeoutS          = 3600
	maxForceOffIntervalS = 86400
	maxActiveThresholdMs = 3600000 // 1h
	maxWatchIntervalMs   = 60000
)

// Default returns a Config populated with sensible defaults.
func Default() Config {
	return Config{
		HA: HAConfig{
			URL:           "",
			Token:         "",
			EntityID:      "input_boolean.screen_power",
			PollIntervalS: 1,
			TimeoutS:      10,
		},
		Screen: ScreenConfig{
			LinuxBackend:      "auto",
			ForceOffIntervalS: 30,
			ActiveThresholdMs: 2000,
			WatchIntervalMs:   250,
		},
		Log: LogConfig{
			Level: "info",
		},
	}
}

// Load reads the JSON config at path and merges it over defaults.
// A missing file is not an error; defaults are used instead.
func Load(path string) (Config, error) {
	cfg := Default()
	if path == "" {
		return cfg, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return cfg, nil
		}
		return cfg, err
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return cfg, err
	}
	cfg = applyDefaults(cfg)
	return cfg, nil
}

// bounded clamps a value into [1, max]; out-of-range values fall back to the
// sane default instead.
func bounded(v, def, max int) int {
	if v <= 0 {
		return def
	}
	if v > max {
		return max
	}
	return v
}

func applyDefaults(cfg Config) Config {
	d := Default()
	if cfg.HA.URL == "" {
		cfg.HA.URL = d.HA.URL
	}
	if cfg.HA.Token == "" {
		cfg.HA.Token = d.HA.Token
	}
	if cfg.HA.EntityID == "" {
		cfg.HA.EntityID = d.HA.EntityID
	}
	cfg.HA.PollIntervalS = bounded(cfg.HA.PollIntervalS, d.HA.PollIntervalS, maxPollIntervalS)
	cfg.HA.TimeoutS = bounded(cfg.HA.TimeoutS, d.HA.TimeoutS, maxTimeoutS)
	if cfg.Screen.LinuxBackend == "" {
		cfg.Screen.LinuxBackend = d.Screen.LinuxBackend
	}
	cfg.Screen.ForceOffIntervalS = bounded(cfg.Screen.ForceOffIntervalS, d.Screen.ForceOffIntervalS, maxForceOffIntervalS)
	cfg.Screen.ActiveThresholdMs = bounded(cfg.Screen.ActiveThresholdMs, d.Screen.ActiveThresholdMs, maxActiveThresholdMs)
	cfg.Screen.WatchIntervalMs = bounded(cfg.Screen.WatchIntervalMs, d.Screen.WatchIntervalMs, maxWatchIntervalMs)
	if cfg.Log.Level == "" {
		cfg.Log.Level = d.Log.Level
	}
	return cfg
}
