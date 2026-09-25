// scroff (HA Screen Off): cross-platform screen on/off controller driven by
// Home Assistant, with an input watchdog that re-wakes the display on mouse or
// keyboard activity.
//
// Usage:
//
//	scroff setup                           interactive config generation
//	                                         (writes ~/.config/scroff/config.json)
//	scroff serve [-d]                      run the watchdog (default); -d runs in background
//	scroff stop                            stop the background watchdog
//	scroff off                             turn the screen off once
//	scroff on                              turn the screen on once
//	scroff status                          print platform/idle/screen/daemon info
//	scroff version                         print the build version
//	scroff help                            print usage summary
//
// A bare `scroff` (no subcommand) prints a setup hint when no config exists,
// and the usage summary otherwise.
//
// Without --config the tool looks for ~/.config/scroff/config.json.
//
// Built with the Go standard library only, so it cross-compiles to
// Windows / macOS / Linux from any host without extra toolchains.
package main

import (
	"context"
	_ "embed"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"scroff/internal/config"
	"scroff/internal/console"
	"scroff/internal/controller"
	"scroff/internal/daemon"
	"scroff/internal/ha"
	"scroff/internal/input"
	"scroff/internal/screen"
	"scroff/internal/setup"
)

//go:embed VERSION
var embeddedVersion string

// version is the single source of truth for the build version: the VERSION
// file embedded above. It can be overridden at build time with
//
//	-ldflags "-X main.version=..."
//
// which is mainly useful for ad-hoc builds; releases just use the embedded one.
var version string

// Version returns the current build version (embedded VERSION file, unless an
// ldflags override was supplied).
func Version() string {
	if v := strings.TrimSpace(version); v != "" {
		return v
	}
	return strings.TrimSpace(embeddedVersion)
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run() error {
	cmd := "serve"
	explicit := false
	args := os.Args[1:]
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		cmd = args[0]
		args = args[1:]
		explicit = true
	}

	// The Windows build is a console-subsystem executable so shells wait for
	// command-line commands to finish. When Task Scheduler, Explorer, or another
	// non-interactive launcher gives scroff a console of its own, Attach
	// detaches and silences that window. A shared interactive terminal is left untouched.
	// The detached -d child must NOT attach: it inherits the log file instead.
	//
	// attached=false therefore marks a hidden or unavailable console. No-op
	// outside Windows.
	attached := true
	if !daemon.Child() {
		attached = console.Attach()
	}

	fs := flag.NewFlagSet("scroff "+cmd, flag.ContinueOnError)
	fs.Usage = func() {
		fmt.Fprintf(fs.Output(), "Usage: scroff %s [flags]\n\nFlags:\n", cmd)
		fs.VisitAll(func(f *flag.Flag) {
			dash := "-"
			if len(f.Name) > 1 {
				dash = "--"
			}
			help := f.Usage
			if f.DefValue != "" {
				help += " (default " + f.DefValue + ")"
			}
			fmt.Fprintf(fs.Output(), "  %s%s    %s\n", dash, f.Name, help)
		})
	}
	configPath := fs.String("config", defaultConfigPath(), "path to JSON config file (default: ~/.config/scroff/config.json)")
	url := fs.String("url", "", "Home Assistant base URL (overrides config)")
	token := fs.String("token", "", "Home Assistant long-lived access token (overrides config)")
	entity := fs.String("entity", "", "entity to watch, e.g. input_boolean.screen_power (overrides config)")
	verbose := fs.Bool("verbose", false, "enable debug logging")
	background := fs.Bool("d", false, "run serve in the background (daemon mode)")
	showVersion := fs.Bool("v", false, "print version and exit")
	showVersionLong := fs.Bool("version", false, "print version and exit")

	// Enforce the dash convention up front: single-letter flags use one dash
	// (-d, -v), everything longer uses two (--config, --verbose). Go's flag
	// package would otherwise accept both forms silently.
	flagKinds := map[string]bool{} // flag name -> takes a separate value
	fs.VisitAll(func(f *flag.Flag) {
		_, isBool := f.Value.(interface{ IsBoolFlag() bool })
		flagKinds[f.Name] = !isBool
	})
	if err := checkFlagStyle(args, flagKinds); err != nil {
		return err
	}
	if err := fs.Parse(args); err != nil {
		return err
	}

	if *showVersion || *showVersionLong {
		cmdVersion()
		return nil
	}

	// Bare invocation (no subcommand): guide the user instead of silently
	// serving - point at setup when no config exists, show usage otherwise.
	if cmd == "serve" && !explicit {
		if _, err := os.Stat(*configPath); err == nil {
			cmdHelp()
		} else {
			fmt.Fprintf(os.Stderr, "no config found at %s (run `scroff setup` to generate it interactively)\n",
				defaultOrSetupHint(*configPath))
		}
		return nil
	}

	// Daemon mode: if this process is the spawned background child, keep going;
	// otherwise re-exec ourselves detached and return.
	//
	// Exception: when launched with a detached or unavailable console (Task
	// Scheduler / autostart), "-d" must NOT detach - a detached child would
	// escape the task's control and could no longer be ended by Task Scheduler.
	// Setting -d there is harmless: scroff just runs as the direct, fully
	// manageable foreground watchdog, windowless and silent. From a real
	// terminal -d detaches as usual.
	if cmd == "serve" && *background && !daemon.Child() && attached {
		return startBackground()
	}

	cfg, err := config.Load(*configPath)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	if *url != "" {
		cfg.HA.URL = *url
	}
	if *token != "" {
		cfg.HA.Token = *token
	}
	if *entity != "" {
		cfg.HA.EntityID = *entity
	}
	if cfg.Log.Verbose {
		cfg.Log.Level = "debug"
	}
	if *verbose {
		cfg.Log.Level = "debug"
	}
	// In headless watchdog runs (scheduled task / autostart: no attached
	// console) keep slog out of the void: write to the daemon log file, so
	// `scroff logs` shows what happened even though nothing is printed. The -d
	// child already writes there via its redirected stdout/stderr.
	setupLogging(cfg.Log.Level, !attached && cmd == "serve")

	switch cmd {
	case "serve":
		return cmdServe(cfg, *configPath)
	case "off", "on":
		return cmdSet(cfg, cmd == "on")
	case "status":
		return cmdStatus(cfg, *configPath)
	case "setup", "init":
		return cmdSetup()
	case "version":
		cmdVersion()
		return nil
	case "help":
		cmdHelp()
		return nil
	case "stop":
		return cmdStop()
	case "logs":
		return cmdLogs()
	default:
		return fmt.Errorf("unknown command %q (want setup|serve|stop|off|on|status|logs)", cmd)
	}
}

// checkFlagStyle enforces the dash convention: single-letter flags are written
// with one dash (-d, -v) and multi-letter flags with two (--config, --verbose).
// Go's flag package accepts either form without complaint, so we validate the
// style here. takesValue marks flags that consume the next argument as their
// value (so that value is not mistaken for another flag).
func checkFlagStyle(args []string, takesValue map[string]bool) error {
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			return nil
		}
		if len(a) < 2 || a[0] != '-' {
			continue
		}
		raw := a
		if eq := strings.IndexByte(raw, '='); eq >= 0 {
			raw = raw[:eq]
		}
		double := strings.HasPrefix(raw, "--")
		name := strings.TrimPrefix(raw, "--")
		if !double {
			name = raw[1:]
		}
		if _, known := takesValue[name]; !known {
			continue // let flag.Parse report genuinely unknown flags
		}
		if (double && len(name) == 1) || (!double && len(name) > 1) {
			dash := "-"
			if len(name) > 1 {
				dash = "--"
			}
			return fmt.Errorf("flag %s: use %q instead", a, dash+name)
		}
		if takesValue[name] && i+1 < len(args) {
			i++ // skip the token that is this flag's value
		}
	}
	return nil
}

// defaultConfigPath returns ~/.config/scroff/config.json if it exists,
// otherwise "" (which makes the loader fall back to built-in defaults).
func defaultConfigPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	p := filepath.Join(home, ".config", "scroff", "config.json")
	if _, err := os.Stat(p); err != nil {
		return ""
	}
	return p
}

// defaultOrSetupHint builds a friendly hint string for a missing config.
func defaultOrSetupHint(configPath string) string {
	if configPath != "" {
		return "config file " + configPath
	}
	return "~/.config/scroff/config.json"
}

// startBackground re-launches this binary as a detached process and returns.
func startBackground() error {
	childArgs := make([]string, 0, len(os.Args)-1)
	for _, a := range os.Args[1:] {
		// Go's flag package accepts both "-d" and "--d"; drop either, the
		// child must not daemonize again.
		if a == "-d" || a == "--d" {
			continue
		}
		childArgs = append(childArgs, a)
	}
	pid, err := daemon.Spawn(childArgs)
	if err != nil {
		return err
	}
	logPath, _ := daemon.LogFile()
	fmt.Printf("scroff running in background (pid %d)\n", pid)
	fmt.Printf("  log: %s\n  stop with: scroff stop\n", logPath)
	return nil
}

// cmdServe runs the Home Assistant-driven watchdog. configPath is only used for
// diagnostics in the startup log line.
func cmdServe(cfg config.Config, configPath string) error {
	if cfg.HA.URL == "" || cfg.HA.Token == "" {
		return fmt.Errorf("no Home Assistant configuration found - run `scroff setup` (or create %s)", defaultOrSetupHint(configPath))
	}
	// Single instance: a Task Scheduler logon/unlock trigger can otherwise
	// start several watchdogs that fight over the screen. The lock dies with
	// the process (flock / exclusive handle), so a forced stop releases it.
	release, err := daemon.Lock()
	if err != nil {
		return fmt.Errorf("refusing to start: %w", err)
	}
	defer release()

	// Record this instance's pid so `scroff stop` can terminate it - whether
	// it runs as a scheduled-task foreground process or as a -d background
	// daemon. Removed again on graceful shutdown; a hard kill (taskkill /F,
	// task End) leaves a stale entry that Stop() detects and cleans up.
	if err := daemon.WritePID(); err != nil {
		slog.Warn("cannot write pid file", "error", err)
	} else {
		defer func() { _ = daemon.RemovePID() }()
	}

	if daemon.Child() {
		slog.Info("background daemon", "pid", os.Getpid())
	}

	scr, err := screen.New(cfg.Screen.LinuxBackend)
	if err != nil {
		return fmt.Errorf("screen backend: %w", err)
	}
	slog.Info("screen backend", "backend", scr.Backend())

	var in input.Watcher
	if w, err := input.New(); err != nil {
		slog.Warn("input watcher unavailable - auto-wake on mouse/keyboard is DISABLED; use a Home Assistant automation or toggle to wake the screen",
			"error", err, "hint", "grant read access to /dev/input (add your user to the 'input' group) or install xprintidle")
		in = input.NewNoop()
	} else {
		in = w
		slog.Info("input watcher ready", "method", input.Method())
	}

	hc, err := ha.New(cfg.HA.URL, cfg.HA.Token, cfg.HA.EntityID, time.Duration(cfg.HA.TimeoutS)*time.Second, cfg.HA.InsecureTLS)
	if err != nil {
		return fmt.Errorf("home assistant client: %w", err)
	}

	c := controller.New(
		scr, in, hc,
		time.Duration(cfg.HA.PollIntervalS)*time.Second,
		time.Duration(cfg.Screen.WatchIntervalMs)*time.Millisecond,
		time.Duration(cfg.Screen.ForceOffIntervalS)*time.Second,
		time.Duration(cfg.Screen.ActiveThresholdMs)*time.Millisecond,
	)

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	slog.Info("scroff started",
		"ha", cfg.HA.URL, "entity", cfg.HA.EntityID, "insecure_tls", cfg.HA.InsecureTLS,
		"config", configPath,
		"force_off_interval_s", cfg.Screen.ForceOffIntervalS,
		"active_threshold_ms", cfg.Screen.ActiveThresholdMs)
	c.Run(ctx)
	return nil
}

// cmdSetup interactively generates the default config file.
func cmdSetup() error {
	path, err := setup.Interactive(os.Stdin, os.Stdout)
	if err != nil {
		return err
	}
	fmt.Printf("Done. Run the watchdog:\n  scroff serve        (foreground)\n  scroff serve -d    (background)\n  scroff stop        (stop the background one)\n")
	fmt.Printf("\nConfig file: %s\n", path)
	return nil
}

// cmdHelp prints the usage summary.
func cmdHelp() {
	fmt.Print(`scroff - HA Screen Off: screen on/off controller driven by Home Assistant

Usage:
  scroff setup          generate the config interactively (~/.config/scroff/config.json)
  scroff serve [-d]     run the watchdog (default); -d runs in background
  scroff stop           stop the background watchdog
  scroff off            turn the screen off once
  scroff on             turn the screen on once
  scroff status         print platform/idle/screen/daemon info
  scroff logs           print the background daemon's log
  scroff version        print the version
  scroff help           show this help

Flags:
  --config PATH         config file (default: ~/.config/scroff/config.json)
  --url/--token/--entity  override the value from config
  -v, --version         print the version and exit
  --verbose             enable debug logging
  -d                    (serve only) run in the background

Without --config the tool looks for ~/.config/scroff/config.json.
Built with the Go standard library only.
`)
}

// cmdVersion prints the build version.
func cmdVersion() {
	fmt.Println(Version())
}

// cmdStop terminates the background daemon.
func cmdStop() error {
	pid, err := daemon.Stop()
	if err != nil {
		return err
	}
	fmt.Printf("stopped scroff (pid %d)\n", pid)
	return nil
}

// cmdLogs prints the background daemon's log.
func cmdLogs() error {
	logPath, err := daemon.LogFile()
	if err != nil {
		return err
	}
	data, err := os.ReadFile(logPath)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("no log file yet at %s (start the daemon with `scroff serve -d`)", logPath)
		}
		return err
	}
	fmt.Print(string(data))
	return nil
}

// cmdSet turns the screen on or off once and exits.
func cmdSet(cfg config.Config, on bool) error {
	scr, err := screen.New(cfg.Screen.LinuxBackend)
	if err != nil {
		return err
	}
	if on {
		return scr.On()
	}
	return scr.Off()
}

// cmdStatus prints diagnostic information about this machine.
func cmdStatus(cfg config.Config, configPath string) error {
	// Config / daemon state first: these work even if the screen backend fails.
	if configPath != "" {
		fmt.Printf("config:            %s\n", configPath)
	} else {
		fmt.Printf("config:            (none found - run `scroff setup`)\n")
	}
	if pid, err := daemon.ReadPID(); err == nil {
		fmt.Printf("background:        running (pid %d)\n", pid)
	} else {
		fmt.Printf("background:        not running\n")
	}
	if lp, err := daemon.LogFile(); err == nil {
		fmt.Printf("daemon log:        %s\n", lp)
	}

	scr, err := screen.New(cfg.Screen.LinuxBackend)
	if err != nil {
		fmt.Printf("screen backend:    ERROR: %v\n", err)
	} else {
		avail := scr.Available()
		fmt.Printf("screen backend:    %s\n", scr.Backend())
		fmt.Printf("backend available: %v\n", avail == nil)
		if avail != nil {
			fmt.Printf("backend error:     %v\n", avail)
		}
		if isOff, err := scr.IsOff(); err == nil {
			fmt.Printf("screen off:        %v\n", isOff)
		} else if err == screen.ErrNoQuery {
			fmt.Printf("screen off:        unknown (platform cannot query)\n")
		} else {
			fmt.Printf("screen off:        query failed: %v\n", err)
		}
	}

	// Probe HA if configured - tells us directly whether the tool can read the
	// entity that is supposed to control the screen.
	if cfg.HA.URL != "" && cfg.HA.Token != "" {
		hc, err := ha.New(cfg.HA.URL, cfg.HA.Token, cfg.HA.EntityID, 5*time.Second, cfg.HA.InsecureTLS)
		if err != nil {
			fmt.Printf("ha client:         invalid config: %v\n", err)
		} else {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			st, stateErr := hc.State(ctx)
			cancel()
			if stateErr != nil {
				fmt.Printf("ha state:          ERROR: %v\n", stateErr)
				fmt.Printf("ha hint:           check url/token, and set ha.insecure_tls if an https/self-signed cert is involved\n")
			} else {
				fmt.Printf("ha entity:         %s\n", cfg.HA.EntityID)
				fmt.Printf("ha state:          %s\n", st)
			}
		}
	} else {
		fmt.Printf("ha:                not configured (add url + token to read entity state)\n")
	}

	in, err := input.New()
	if err != nil {
		fmt.Printf("input watcher:     unavailable (%v)\n", err)
	} else {
		defer in.Close()
		fmt.Printf("input method:      %s\n", input.Method())
		fmt.Printf("idle time:         %s\n", in.IdleSince().Round(time.Millisecond))
	}
	if runtime.GOOS == "windows" {
		fmt.Printf("note:              Windows screen control works only from an interactive desktop session (not SSH/service/non-interactive terminals)\n")
	}
	return nil
}

// setupLogging configures the slog default handler to the requested level.
// In headless watchdog runs (toFile=true) log lines go to
// ~/.config/scroff/scroff.log instead of a (nonexistent) terminal.
func setupLogging(level string, toFile bool) {
	var lvl slog.Level
	switch strings.ToLower(level) {
	case "debug":
		lvl = slog.LevelDebug
	case "warn":
		lvl = slog.LevelWarn
	case "error":
		lvl = slog.LevelError
	default:
		lvl = slog.LevelInfo
	}
	out := io.Writer(os.Stderr)
	if toFile {
		if lf, err := daemon.OpenLog(); err == nil {
			out = lf
		}
	}
	slog.SetDefault(slog.New(slog.NewTextHandler(out, &slog.HandlerOptions{Level: lvl})))
}
