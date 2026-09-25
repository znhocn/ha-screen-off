// Package ha talks to a Home Assistant instance over its REST API.
//
// The tool watches a binary entity (switch/input_boolean/light/...) - usually
// created by the user - and mirrors its state to the physical screen:
//
//	entity off -> screen off (then the input watchdog re-wakes on activity)
//	entity on  -> screen on
//
// When the watchdog wakes the screen due to user input it calls the entity's
// turn_on service so Home Assistant's state stays in sync.
//
// REST polling is used instead of the WebSocket API so the binary ships with
// zero external dependencies.
package ha

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync/atomic"
	"time"
)

const (
	// StateOn / StateOff are the relevant Home Assistant entity states.
	StateOn  = "on"
	StateOff = "off"
)

// Client is a thin Home Assistant REST client for one entity.
type Client struct {
	baseURL  string
	token    string
	entityID string
	domain   string
	http     *http.Client // request calls (State / service calls), has a total timeout
	stream   *http.Client // long-lived /api/stream connection, no total timeout

	lastLog atomic.Int64 // unix nano of the last rate-limited error log
	modeLog string       // last monitoring mode we logged
}

// New builds a Client. The entity's domain is derived from its entity_id.
// insecureTLS skips TLS certificate verification (for Home Assistant instances
// exposed over https with a self-signed certificate).
func New(baseURL, token, entityID string, timeout time.Duration, insecureTLS bool) (*Client, error) {
	if baseURL == "" || token == "" || entityID == "" {
		return nil, errors.New("ha: url, token and entity_id are required")
	}
	baseURL = strings.TrimRight(baseURL, "/")
	dot := strings.IndexByte(entityID, '.')
	if dot < 1 {
		return nil, fmt.Errorf("ha: invalid entity_id %q (want domain.name)", entityID)
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	if insecureTLS {
		transport.TLSClientConfig = &tls.Config{InsecureSkipVerify: true} //nolint:gosec // user opted in via config
	}
	// c.http gets a per-request timeout; c.stream must not, or the long-lived
	// event stream would be torn down mid-request.
	return &Client{
		baseURL:  baseURL,
		token:    token,
		entityID: entityID,
		domain:   entityID[:dot],
		http:     &http.Client{Timeout: timeout, Transport: transport},
		stream:   &http.Client{Transport: transport},
	}, nil
}

// State returns the entity's current state ("on"/"off"/"unavailable"/...).
func (c *Client) State(ctx context.Context) (string, error) {
	var out struct {
		State string `json:"state"`
	}
	if err := c.do(ctx, http.MethodGet, "/api/states/"+c.entityID, nil, &out); err != nil {
		return "", err
	}
	return out.State, nil
}

// TurnOn turns the entity on (e.g. input_boolean.turn_on).
func (c *Client) TurnOn(ctx context.Context) error {
	return c.callService(ctx, c.domain+"/turn_on")
}

// TurnOff turns the entity off.
func (c *Client) TurnOff(ctx context.Context) error {
	return c.callService(ctx, c.domain+"/turn_off")
}

func (c *Client) callService(ctx context.Context, service string) error {
	body := map[string]string{"entity_id": c.entityID}
	return c.do(ctx, http.MethodPost, "/api/services/"+service, body, nil)
}

// do performs a JSON request. Calls Home Assistant's REST API and returns a
// detailed error (including HA's error message) on non-2xx.
func (c *Client) do(ctx context.Context, method, path string, payload any, out any) error {
	var body io.Reader
	if payload != nil {
		b, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		body = strings.NewReader(string(b))
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, body)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode >= 400 {
		return fmt.Errorf("ha: %s %s -> %s: %s", method, path, resp.Status, strings.TrimSpace(string(data)))
	}
	if out != nil && len(data) > 0 {
		return json.Unmarshal(data, out)
	}
	return nil
}

// Stream/monitoring constants for the Home Assistant /api/stream endpoint.
const (
	streamPath        = "/api/stream"
	streamIdleTimeout = 75 * time.Second // no event for this long => rotate the connection (HA pings ~every 50s)
	streamRetryDelay  = 2 * time.Second
)

// errStreamIdle marks a connection rotated because no events arrived within
// streamIdleTimeout - a normal refresh, not a failure.
var errStreamIdle = errors.New("home assistant event stream idle")

// Poll reports entity state changes to onChange. It drives two layers:
//
//   - a guaranteed polling loop every interval - the safety net. Even when a
//     reverse proxy buffers or blocks the event stream (the connection opens
//     with HTTP 200 but no events ever flow), toggles are still caught within
//     pollInterval;
//   - a subscription to Home Assistant's /api/stream (server-sent events) on
//     top of it, which accelerates reaction to milliseconds when the stream
//     does deliver.
//
// The current mode is logged for diagnostics. Poll blocks until ctx is done.
func (c *Client) Poll(ctx context.Context, interval time.Duration, onChange func(state string)) {
	// Always-on polling floor below the event stream.
	go c.pollLoop(ctx, interval, onChange)

	for ctx.Err() == nil {
		err := c.streamAndRead(ctx, onChange)
		if ctx.Err() != nil {
			return
		}
		if err == errStreamIdle {
			// No events for a long while (proxies can silently swallow idle
			// connections). Just reconnect; the poll loop covers the gap.
			continue
		}
		c.setMode("polling")
		if time.Since(time.Unix(0, c.lastLog.Load())) > 30*time.Second {
			slog.Warn("home assistant event stream error - continuing with polling", "error", err)
			c.lastLog.Store(time.Now().UnixNano())
		}
		select {
		case <-time.After(streamRetryDelay):
		case <-ctx.Done():
			return
		}
	}
}

// setMode logs monitoring-mode transitions once.
func (c *Client) setMode(m string) {
	if c.modeLog != m {
		c.modeLog = m
		slog.Info("home assistant monitoring", "mode", m, "hint", "polling reacts within the poll interval; the event stream is near-instant")
	}
}

// streamAndRead keeps a connection to /api/stream open and feeds entity state
// changes to onChange until the connection ends or ctx is cancelled.
func (c *Client) streamAndRead(ctx context.Context, onChange func(state string)) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+streamPath, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("Cache-Control", "no-cache")
	resp, err := c.stream.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		io.Copy(io.Discard, io.LimitReader(resp.Body, 4<<10))
		return fmt.Errorf("ha: GET %s -> %s", streamPath, resp.Status)
	}
	c.setMode("stream")

	// Rotate the connection when it is idle for too long (covers proxies that
	// silently kill streams) and abort it promptly on shutdown. Closing the
	// body unblocks the read loop below. The goroutine is released when
	// streamAndRead returns (via released), and the timer is stopped then too,
	// so a fast-failing stream does not pile up idle timers/goroutines.
	idle := time.NewTimer(streamIdleTimeout)
	released := make(chan struct{})
	defer func() {
		close(released)
		idle.Stop()
	}()
	var rotated atomic.Bool
	go func() {
		select {
		case <-ctx.Done():
			resp.Body.Close()
		case <-idle.C:
			rotated.Store(true)
			resp.Body.Close()
		case <-released:
		}
	}()

	rd := bufio.NewReader(resp.Body)
	var evType, data string
	for {
		line, err := rd.ReadString('\n')
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if rotated.Load() {
				return errStreamIdle
			}
			return err
		}
		line = strings.TrimRight(line, "\r\n")
		switch {
		case strings.HasPrefix(line, "event: "):
			evType = strings.TrimPrefix(line, "event: ")
		case strings.HasPrefix(line, "data: "):
			data = strings.TrimPrefix(line, "data: ")
		case line == "":
			if evType == "state_changed" && data != "" {
				if st, ok := streamEntityState(data, c.entityID); ok {
					slog.Debug("ha event", "entity", c.entityID, "state", st)
					onChange(st)
				}
			}
			evType, data = "", ""
		}
	}
}

// streamEntityState extracts the new entity state from an SSE state_changed
// event payload. ok is false when the event does not concern our entity.
func streamEntityState(data, entityID string) (string, bool) {
	var ev struct {
		EntityID string `json:"entity_id"`
		NewState *struct {
			State string `json:"state"`
		} `json:"new_state"`
	}
	if err := json.Unmarshal([]byte(data), &ev); err != nil {
		return "", false
	}
	if ev.EntityID != entityID || ev.NewState == nil {
		return "", false
	}
	return ev.NewState.State, true
}

// syncOnce fetches the current entity state once and reports it on success.
func (c *Client) syncOnce(ctx context.Context, onChange func(state string)) bool {
	st, err := c.State(ctx)
	if err != nil {
		return false
	}
	onChange(st)
	return true
}

// pollLoop polls State() every interval and reports changes to onChange until
// ctx is done. Errors are logged (rate-limited) and the last known state is
// preserved across outages.
func (c *Client) pollLoop(ctx context.Context, interval time.Duration, onChange func(state string)) {
	c.syncOnce(ctx, onChange)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			st, err := c.State(ctx)
			if err != nil {
				if time.Since(time.Unix(0, c.lastLog.Load())) > 30*time.Second {
					slog.Warn("home assistant unreachable", "error", err,
						"hint", "check url/token, and set ha.insecure_tls if an https/self-signed cert is involved")
					c.lastLog.Store(time.Now().UnixNano())
				}
				continue
			}
			slog.Debug("ha state", "entity", c.entityID, "state", st)
			onChange(st)
		}
	}
}
