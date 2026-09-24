# HA Screen Off (scroff)

>  [English](README.md) | [中文](README.zh-CN.md)

A zero-dependency, cross-platform **display on/off controller driven by Home
Assistant**, with an input watchdog that **auto-wakes the screen the moment you
touch the mouse or keyboard**. Compiles to a single `scroff` binary for
**Windows, macOS and Linux**. Through the HomeKit Bridge it can also be exposed to HomeKit, enabling Siri voice control.

## Features

- **Home Assistant driven** - toggle one entity and the screen follows.
- **Instant reaction** - subscribes to HA's `/api/stream` (server-sent
  events); a toggle is acted on in **milliseconds**, with a guaranteed polling
  floor should a reverse proxy buffer or block the stream.
- **Auto-wake on input** - any mouse/keyboard activity exits off-mode and
  wakes the display immediately (only input *after* the screen-off counts).
- **Fights unwanted wakes** - no input means the screen stays off: every
  `force_off_interval_s` the tool re-asserts power-off against notifications or
  background apps that turn it back on.
- **Single zero-dependency binary** - Go standard library only, no runtimes or
  C toolchains, cross-compiles from any host.
- **Background mode** - `serve -d` with PID file, log file, `stop`/`logs`/
  `status` diagnostics.

## How it works

```
Home Assistant (input_boolean.screen_power)  <-- state via REST + event stream
        │  "off" =====//========================>           ┌────────────────┐
        │                                                   │  scroff        │
        │         "on" ===================================> │  ─  reads state│
        │                                                   │  ─  controls   │
        └── input_boolean.turn_on (when widget wakes) <─────│     display    │
                                                            └───────┬────────┘
                              mouse / keyboard activity ────────────┘
                                     (watched continuously)
```

- **entity `off`** → the screen is turned off and the tool enters *off-mode*.
- **while off-mode is active** any mouse/keyboard activity exits off-mode,
  wakes the screen, and flips the Home Assistant entity back to `on` so HA
  stays in sync.
- **otherwise** (no input) the screen is kept **off**: every
  `force_off_interval_s` it re-asserts power-off to defeat unwanted wakes such
  as a chat/notification or a background app turning the display back on.
- **entity `on`** → the screen is turned on.

So: *if you interact, the screen wakes; otherwise it stays dark.*

### State delivery: event stream + polling fallback

The tool subscribes to Home Assistant's `/api/stream` (**server-sent events**),
so a toggle is acted on in **milliseconds** instead of waiting for the next
poll. A **background poll every `ha.poll_interval_s` always runs underneath as
a safety net** - so even if a reverse proxy silently buffers or blocks the
event stream (`mode=stream` connects with HTTP 200 but no events ever arrive),
toggles are still caught within the poll interval. The current stream state is
shown in the log via `msg="home assistant monitoring" mode=stream|polling`.

## Build

Go ≥ 1.22 is the only requirement. Everything is standard library - no `go mod
download` or C toolchains needed, and every target cross-compiles from a single
machine:

```sh
# Linux (X11 via xset, or Wayland via wlopm)
GOOS=linux  GOARCH=amd64 go build -o bin/scroff-linux  .

# Windows
GOOS=windows GOARCH=amd64 go build -o bin/scroff.exe  .

# macOS (Intel / Apple Silicon)
GOOS=darwin  GOARCH=amd64 go build -o bin/scroff-mac   .
GOOS=darwin  GOARCH=arm64 go build -o bin/scroff-mac-m1 .
```

Pre-built binaries can also be dropped into `bin/`.

## Usage

```sh
scroff setup              # interactive config generation -> ~/.config/scroff/config.json
scroff serve              # run the watchdog (foreground, default command)
scroff serve -d           # run the watchdog in the background (daemon mode)
scroff stop               # stop the background watchdog
scroff status             # print config/daemon/backend/HA/input diagnostics
scroff logs               # show the background daemon's log
scroff off                # turn the screen off once
scroff on                 # turn the screen on once
scroff help               # show this usage summary
scroff version            # print the version (same as -v or -version)
```

Flags: `-config PATH` choose the config file, `-url`/`-token`/`-entity` override
values from config, `-verbose` enables debug logging, `-d` runs `serve` in the
background, `-v`/`-version` print the version.

Running bare `scroff` (no subcommand) prints the usage summary; if no config
file exists yet it instead points you at `scroff setup`.

If no config file is given (`-config`), the tool automatically uses
`~/.config/scroff/config.json` - the same file `setup` writes. So the
typical flow is:

```sh
scroff setup      # answer the questions (defaults shown in [brackets])
scroff serve -d   # run it in the background
scroff logs       # check it started / debug
scroff status     # one-line overview whenever something looks wrong
scroff stop       # stop it
```

Background mode writes a PID file and logs to
`~/.config/scroff/scroff.log`; `stop` terminates the daemon - on Linux/macOS
the SIGTERM handler also restores the screen to **on**; on Windows the process
is force-killed (`taskkill /F`), so there the Home Assistant entity is the
source of truth for the restore.

## Home Assistant setup

1. In Home Assistant create a **helper** (`Settings → Devices & Services →
   Helpers → Add Helper → Toggle`). Name it for example *Screen Power* → entity
   `input_boolean.screen_power`.
2. Create a **long-lived access token** (click your user → Security →
   Long-Lived Access Tokens).
3. Put both plus your HA URL into a config file (see
   `examples/config.json`):

```json
{
  "ha": {
    "url": "http://homeassistant.local:8123",
    "token": "LONG_LIVED_ACCESS_TOKEN",
    "entity_id": "input_boolean.screen_power"
  }
}
```

4. Run `scroff setup` and answer the questions (or write the
   `examples/config.json` file yourself), then `scroff serve` - use
   `serve -d` to run it in the background.

Turn the toggle **off** in HA (dashboard / automation / voice) and the screen
goes dark. When you touch the mouse the tool wakes the screen **and flips the
toggle back to on** automatically.

> The entity can be any binary entity (`switch.*`, `light.*`, `input_boolean.*`,
> ...). The tool derives the domain and calls the matching `turn_on`/`turn_off`
> service. Only `on`/`off` states are meaningful - anything else is ignored.

## Platform support & requirements

| Platform | Screen off/on | Idle detection | Requirements |
|---|---|---|---|
| Windows | `SC_MONITORPOWER` + `SetThreadExecutionState` | `GetLastInputInfo` | none (built-in) |
| macOS | `pmset displaysleepnow` / `caffeinate -u` | `ioreg` `HIDIdleTime` | none (built-in) |
| Linux X11 | `xset dpms force off/on` | evdev `/dev/input` (or `xprintidle`) | `xset` |
| Linux Wayland | `wlopm --off/--on` | evdev `/dev/input` | `wlopm` |
| Linux DDC/CI | `ddcutil setvcp 0xD6 ...` (opt-in) | evdev `/dev/input` | `ddcutil` |

Platform notes:

- **Windows** screen control only works from an **interactive desktop
  session** - running via SSH or as a service/non-interactive terminal cannot
  reach the desktop's windows. Task Scheduler "run at logon" works.
- **Linux evdev** detection works on both X11 and Wayland with no
  display-server dependency, but reading `/dev/input/event*` needs read access
  - add your user to the `input` group (`sudo usermod -aG input $USER`) or run
  as root. If the devices can't be opened and `xprintidle` is installed on X11,
  it is used automatically as a fallback.
- The Linux backend is chosen per session automatically (`auto`) from
  `XDG_SESSION_TYPE` (a Wayland session always uses the Wayland backend - `xset`
  only controls XWayland, not the real outputs), and can be forced with
  `"linux_backend": "x11"`, `"wayland"` or `"ddc"`. DDC/CI is opt-in because it
  toggles the monitor's own power rather than the desktop's display power.
- On X11/macOS the tool can *verify* the display really is off
  (`xset q`, `ioreg`); on Windows and DDC it re-asserts power-off periodically
  instead of querying.
- **Multiple monitors** are always controlled **together** - screen off/on is
  global (the Windows broadcast targets every display, `wlopm` uses `*`, `xset
  dpms` covers the whole server), and any input wakes them all. Per-display
  selection is not supported.

## Configuration reference

| Key | Default | Meaning |
|---|---|---|
| `ha.url` | *(required)* | Home Assistant base URL |
| `ha.token` | *(required)* | Long-lived access token |
| `ha.entity_id` | `input_boolean.screen_power` | Entity that controls the screen |
| `ha.poll_interval_s` | `1` | Poll fallback interval (event stream needs none) |
| `ha.timeout_s` | `10` | HTTP timeout for HA calls |
| `ha.insecure_tls` | `false` | Set `true` when HA is on https with a self-signed cert |
| `screen.linux_backend` | `auto` | `auto` \| `x11` \| `wayland` \| `ddc` |
| `screen.force_off_interval_s` | `30` | Re-assert off interval while waiting |
| `screen.active_threshold_ms` | `2000` | Idle time below this = user is active → wake |
| `screen.watch_interval_ms` | `250` | Watchdog resolution in off-mode |
| `log.level` | `info` | `debug` \| `info` \| `warn` \| `error` |

## Run automatically

**Linux (systemd)**

```ini
# /etc/systemd/system/scroff.service
[Unit]
Description=scroff (HA Screen Off) display controller
After=network-online.target

[Service]
ExecStart=/usr/local/bin/scroff serve -config /etc/scroff/config.json
Restart=always
RestartSec=3

[Install]
WantedBy=multi-user.target
```

**macOS (launchd)** - a `KeepAlive` LaunchAgent pointing at the binary and
config.

**Windows** - Task Scheduler (run at logon) or NSSM wrapping
`scroff.exe serve -config config.json`.

## Troubleshooting

Run `scroff status` first - it reports config path, background pid, screen
backend, live HA entity state and input watcher in one shot.

- **No reaction when toggling HA** - check `scroff logs` for
  `home assistant state changed`; if missing, the entity you toggle is not the
  configured `ha.entity_id`. With `-verbose` you also see per-event `ha event`
  lines.
- **`mode=polling` in the log** - the HA reverse proxy does not support the
  `/api/stream` SSE endpoint (buffered/blocked). Polling still works; allow
  streaming responses (GET `/api/stream`) for instant mode. The tool also needs
  GET `/api/states/*` and POST `/api/services/*` (the latter keeps the HA
  switch in sync after an auto-wake; if blocked you get a `failed to sync`
  warning but the screen still wakes).
- **Windows: screen never changes** - make sure it runs in an interactive
  desktop session (logon task), not SSH/service.
- **Linux: mouse does not wake the screen** - the input watcher is disabled
  (see the startup warning). Add your user to the `input` group or install
  `xprintidle`.
- **https with a self-signed certificate** - set `"insecure_tls": true`.

## Notes

- Stopping the process (`SIGINT`/`SIGTERM`) restores the screen to **on**.
- If Home Assistant is unreachable the tool keeps the last known behaviour and
  logs (rate-limited) until HA is back.
- If the input watcher cannot start (e.g. `/dev/input` not readable), the tool
  still runs but auto-wake is disabled - it warns once at startup.
- Requirement checks are done at startup - run `scroff status` to
  diagnose a machine before wiring it up. It also reports the live HA entity
  state, so a bad URL/token/entity or a self-signed https cert shows up
  immediately.
