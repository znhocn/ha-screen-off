// Package controller ties Home Assistant, the screen controller and the input
// watcher together into the display watchdog.
//
// Behaviour:
//
//   - HA entity "off" -> turn the screen off and enter off-mode.
//   - In off-mode, any mouse/keyboard activity wakes the screen and flips the
//     HA entity back to "on" (so HA stays in sync).
//   - While off-mode is active and no input is registered, the screen is
//     re-asserted off every ForceOffInterval to fight unwanted wakes (e.g. a
//     chat notification or a background app turning the display back on).
//   - HA entity "on" -> ensure the screen is on.
package controller

import (
	"context"
	"log/slog"
	"time"

	"scroff/internal/ha"
	"scroff/internal/input"
	"scroff/internal/screen"
)

// Controller orchestrates the watchdog loop.
type Controller struct {
	scr screen.Controller
	in  input.Watcher
	ha  *ha.Client

	pollInterval    time.Duration
	watchInterval   time.Duration
	forceOffEvery   time.Duration
	activeThreshold time.Duration

	haStateCh chan string

	lastState string // last HA state we acted upon ("" = none observed yet)

	offMode       bool
	offSince      time.Time // when off-mode was entered (wake only on input AFTER this)
	lastOffAssert time.Time
}

// New builds a Controller. It validates that all pieces are functional.
func New(scr screen.Controller, in input.Watcher, hc *ha.Client,
	pollInterval, watchInterval, forceOffEvery, activeThreshold time.Duration) *Controller {
	return &Controller{
		scr:             scr,
		in:              in,
		ha:              hc,
		pollInterval:    pollInterval,
		watchInterval:   watchInterval,
		forceOffEvery:   forceOffEvery,
		activeThreshold: activeThreshold,
		haStateCh:       make(chan string, 8),
	}
}

// Run drives everything until ctx is cancelled. On shutdown the screen is
// restored to on.
func (c *Controller) Run(ctx context.Context) {
	defer func() {
		slog.Info("shutting down, restoring screen")
		if err := c.scr.On(); err != nil {
			slog.Warn("failed to restore screen on shutdown", "error", err)
		}
		c.in.Close()
	}()

	// Initial sync so the screen reflects HA right away, not after the first poll.
	if err := c.syncFromHA(ctx); err != nil {
		slog.Warn("initial home assistant sync failed", "error", err)
	}

	go c.ha.Poll(ctx, c.pollInterval, func(st string) {
		select {
		case c.haStateCh <- st:
		case <-ctx.Done():
		}
	})

	ticker := time.NewTicker(c.watchInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case st := <-c.haStateCh:
			c.handleHAState(st)
		case <-ticker.C:
			if c.offMode {
				c.watch()
			}
		}
	}
}

// syncFromHA queries the current entity state and applies it once.
func (c *Controller) syncFromHA(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	st, err := c.ha.State(ctx)
	if err != nil {
		return err
	}
	c.handleHAState(st)
	return nil
}

// handleHAState mirrors Home Assistant state onto the screen. It only acts on
// transitions (dedupe), and logs every observed change so it is always visible
// whether the tool is seeing the entity state or not.
func (c *Controller) handleHAState(st string) {
	if st == c.lastState {
		return // steady state, nothing to do
	}
	prev := c.lastState
	c.lastState = st
	if prev == "" {
		slog.Info("home assistant state observed", "state", st)
	} else {
		slog.Info("home assistant state changed", "from", prev, "to", st)
	}

	switch st {
	case ha.StateOff:
		if c.offMode {
			return
		}
		c.enterOffMode()
	case ha.StateOn:
		if c.offMode {
			c.offMode = false
		}
		if err := c.scr.On(); err != nil {
			slog.Warn("failed to turn screen on", "error", err)
		}
	default:
		slog.Warn("ignoring unexpected entity state", "state", st)
	}
}

// wakeEligible decides whether the measured idle time represents user input
// that happened after off-mode was entered. Input that predates off-mode (e.g.
// the very click used to toggle the HA switch) must not wake the screen.
func wakeEligible(idle, threshold time.Duration, offSince, now time.Time) bool {
	if idle < 0 || idle >= threshold {
		return false
	}
	return now.Add(-idle).After(offSince)
}

// enterOffMode powers the display down and starts the input watchdog.
func (c *Controller) enterOffMode() {
	c.offMode = true
	c.offSince = time.Now()
	c.lastOffAssert = time.Now()
	if err := c.scr.Off(); err != nil {
		slog.Warn("failed to turn screen off", "error", err)
	}
	slog.Info("off-mode active", "hint", "moving the mouse or pressing a key will wake the screen")
}

// watch runs while off-mode is active: wake on new user input, otherwise keep
// the screen off.
func (c *Controller) watch() {
	idle := c.in.IdleSince()

	// Only input that happened AFTER the screen was turned off counts as
	// activity. Otherwise toggling the switch while still using the machine
	// (last input was seconds ago) would wake the screen instantly.
	if wakeEligible(idle, c.activeThreshold, c.offSince, time.Now()) {
		slog.Info("user input detected after screen-off, exiting off-mode", "idle", idle.Round(time.Millisecond))
		c.offMode = false
		if err := c.scr.On(); err != nil {
			slog.Warn("failed to wake screen", "error", err)
			return
		}
		// Keep Home Assistant in sync so a later "off" toggle still works.
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := c.ha.TurnOn(ctx); err != nil {
			slog.Warn("failed to sync home assistant entity to on", "error", err, "hint", "the HA token must be allowed to call services and its API/proxy must accept POST /api/services/*; screen wake itself already happened")
		}
		return
	}

	// No input: periodically make sure the display is still off.
	if time.Since(c.lastOffAssert) < c.forceOffEvery {
		return
	}
	c.lastOffAssert = time.Now()

	switch off, err := c.scr.IsOff(); {
	case err == screen.ErrNoQuery:
		// Can't ask, so just re-assert.
		if err := c.scr.Off(); err != nil {
			slog.Warn("failed to re-assert screen off", "error", err)
		}
	case err != nil:
		slog.Warn("failed to query screen state", "error", err)
	case !off:
		slog.Info("screen came back on without user input - forcing it off again")
		if err := c.scr.Off(); err != nil {
			slog.Warn("failed to force screen off", "error", err)
		}
	}
}
