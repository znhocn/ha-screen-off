package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadDefaults(t *testing.T) {
	cfg := Default()
	if cfg.HA.PollIntervalS != 1 {
		t.Fatalf("default poll interval = %d", cfg.HA.PollIntervalS)
	}
	if cfg.Screen.ForceOffIntervalS != 30 {
		t.Fatalf("default force off interval = %d", cfg.Screen.ForceOffIntervalS)
	}
	if cfg.HA.EntityID != "input_boolean.screen_power" {
		t.Fatalf("default entity = %q", cfg.HA.EntityID)
	}
}

func TestLoadMergesFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	content := `{
		"ha": {"url": "http://ha:8123", "token": "x", "poll_interval_s": 5},
		"screen": {"force_off_interval_s": 60}
	}`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.HA.URL != "http://ha:8123" || cfg.HA.Token != "x" {
		t.Fatalf("ha fields not loaded: %+v", cfg.HA)
	}
	if cfg.HA.PollIntervalS != 5 {
		t.Fatalf("poll interval = %d", cfg.HA.PollIntervalS)
	}
	if cfg.Screen.ForceOffIntervalS != 60 {
		t.Fatalf("force off = %d", cfg.Screen.ForceOffIntervalS)
	}
	// untouched fields keep defaults
	if cfg.HA.EntityID != "input_boolean.screen_power" {
		t.Fatalf("default entity lost: %q", cfg.HA.EntityID)
	}
	if cfg.Screen.WatchIntervalMs != 250 {
		t.Fatalf("default watch interval lost: %d", cfg.Screen.WatchIntervalMs)
	}
}

func TestLoadMissingFileIsDefault(t *testing.T) {
	cfg, err := Load(filepath.Join(t.TempDir(), "nope.json"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.HA.EntityID == "" {
		t.Fatal("expected defaults on missing file")
	}
}

func TestBadJSON(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bad.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil {
		t.Fatal("expected error for bad JSON")
	}
}
