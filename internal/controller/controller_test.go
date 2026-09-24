package controller

import (
	"testing"
	"time"
)

// shake is a fixed "now" so tests are deterministic.
var shake = time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)

func TestWakeEligible(t *testing.T) {
	const threshold = 2 * time.Second
	cases := []struct {
		name     string
		idle     time.Duration
		offSince time.Time // when the screen was turned off
		want     bool
	}{
		// User moves the mouse 500ms after the screen went off -> wake.
		{"new input after off-mode", 500 * time.Millisecond, shake.Add(-1 * time.Second), true},
		// Input happened BEFORE the screen went off (e.g. the toggle click).
		{"input predates off-mode", 500 * time.Millisecond, shake.Add(2 * time.Second), false},
		// Long idle (user walked away) -> no wake.
		{"long idle", 2 * time.Minute, shake.Add(-5 * time.Second), false},
		// Exact-threshold idle -> no wake.
		{"idle equals threshold", 2 * time.Second, shake.Add(-3 * time.Second), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := wakeEligible(tc.idle, threshold, tc.offSince, shake); got != tc.want {
				t.Fatalf("wakeEligible(%v,%v,%v) = %v, want %v", tc.idle, tc.offSince, shake, got, tc.want)
			}
		})
	}
}
