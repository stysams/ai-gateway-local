// Package app wires the headless gateway process together: CLI parsing, data
// root discovery, config bootstrap, single-instance locking, PID metadata,
// HTTP serving and graceful shutdown. This is task package A's scope; later
// packages add secret, routing, adapters, logging, point and autostart.
package app

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"strconv"
	"time"

	"ai-gateway/internal/autostart"
	"ai-gateway/internal/config"
	"ai-gateway/internal/logstore"
	"ai-gateway/internal/process"
	"ai-gateway/internal/secret"
	"ai-gateway/internal/server"
	"ai-gateway/internal/version"
)

// Unified CLI exit codes (docs/v1-scheme.md §14.1).
const (
	ExitOK         = 0 // success
	ExitError      = 1 // general runtime error
	ExitUsage      = 2 // argument error
	ExitConfig     = 3 // configuration error
	ExitNotRunning = 4 // gateway not running or unreachable
	ExitPartial    = 5 // partial success, manual action required
)

// stdout and stderr are variables so tests can capture CLI output.
var (
	stdout io.Writer = os.Stdout
	stderr io.Writer = os.Stderr
)

// Main is the CLI entry point. It returns the process exit code instead of
// calling os.Exit, which keeps it testable.
func Main(args []string) int {
	if len(args) == 0 {
		usage(stderr)
		return ExitUsage
	}
	switch args[0] {
	case "serve":
		return runServe(args[1:])
	case "stop":
		return runStop(args[1:])
	case "status":
		return runStatus(args[1:])
	case "doctor":
		return runDoctor(args[1:])
	case "autostart":
		return runAutostart(args[1:])
	case "version":
		return runVersion(args[1:])
	case "help", "-h", "--help":
		usage(stdout)
		return ExitOK
	default:
		fmt.Fprintf(stderr, "ai-gateway: unknown command %q\n", args[0])
		usage(stderr)
		return ExitUsage
	}
}

func usage(w io.Writer) {
	fmt.Fprint(w, `usage: ai-gateway <command> [flags]

commands:
  serve        run the gateway in the foreground (listens on 127.0.0.1)
  stop         request a graceful shutdown via the management API
  status       show gateway status
  doctor       show the live diagnostic report
  autostart    enable or disable current-user login start (on|off)
  version      print version information
  help         show this help

serve flags:
  --port N     override the configured listen port (1024-65535)

exit codes:
  0 success, 1 runtime error, 2 usage error, 3 config error, 4 not running,
  5 partial success requiring manual repair
`)
}

func runServe(args []string) int {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	fs.SetOutput(stderr)
	portFlag := fs.Int("port", 0, "override the configured listen port (1024-65535)")
	if err := fs.Parse(args); err != nil {
		return ExitUsage
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(stderr, "serve: unexpected arguments: %v\n", fs.Args())
		return ExitUsage
	}

	dataDir := DefaultDataDir()
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		fmt.Fprintf(stderr, "serve: cannot create data directory %s: %v\n", dataDir, err)
		return ExitError
	}

	mgr := config.NewManager(config.ConfigPath(dataDir))
	cfg, err := mgr.LoadOrCreate()
	if err != nil {
		// The error already carries the file path and locatable fields.
		fmt.Fprintf(stderr, "serve: %v\n", err)
		return ExitConfig
	}
	fmt.Fprintln(stderr, logstore.RiskNotice)

	port := cfg.Listen.PortValue()
	if *portFlag != 0 {
		if *portFlag < 1024 || *portFlag > 65535 {
			fmt.Fprintf(stderr, "serve: --port must be between 1024 and 65535, got %d\n", *portFlag)
			return ExitUsage
		}
		port = *portFlag
	}
	addr := fmt.Sprintf("127.0.0.1:%d", port)

	lock, err := process.AcquireLock(process.LockPath(dataDir))
	if err != nil {
		if errors.Is(err, process.ErrAlreadyRunning) {
			existing := existingListen(process.PIDPath(dataDir), addr)
			fmt.Fprintf(stderr, "serve: ai-gateway is already running and listening on %s\n", existing)
			return ExitError
		}
		fmt.Fprintf(stderr, "serve: %v\n", err)
		return ExitError
	}
	defer lock.Release()

	// The PID file must exist while running and be gone on every exit path:
	// startup failures, Serve errors and graceful shutdown. A single deferred
	// cleanup covers all of them and is a no-op where the file was never
	// written (docs/v1-scheme.md §4: pid.json is diagnostic only).
	pidPath := process.PIDPath(dataDir)
	defer func() {
		if err := os.Remove(pidPath); err != nil && !errors.Is(err, os.ErrNotExist) {
			fmt.Fprintf(stderr, "serve: warning: cannot remove pid file %s: %v\n", pidPath, err)
		}
	}()

	// A provider that requires a key must have a readable secret before the
	// gateway serves traffic (docs/v1-scheme.md §6.2): a missing or
	// unavailable system key store is a startup failure with a remediation
	// hint, never a silent fallback to plaintext. Keyless providers do not
	// depend on the store.
	store := secret.New(dataDir)
	if server.HasRequiredSecrets(cfg) {
		if err := server.CheckSecretStore(context.Background(), store); err != nil {
			fmt.Fprintf(stderr, "serve: %v\n", err)
			fmt.Fprintln(stderr, "serve: 修复：ai-gateway 绝不会回退为明文存储；请配置当前平台的系统钥匙存储（Windows DPAPI / macOS Keychain / Linux Secret Service），或移除需要钥匙的 provider")
			return ExitConfig
		}
		if missing := server.CheckRequiredSecrets(context.Background(), store, cfg); len(missing) > 0 {
			for _, e := range missing {
				fmt.Fprintf(stderr, "serve: %s\n", e)
			}
			fmt.Fprintln(stderr, "serve: 修复：通过管理 API 写入钥匙（POST /api/v1/providers，api_key 字段），然后重新启动")
			return ExitConfig
		}
	}

	srv := server.New(mgr, store, version.Version, os.Getpid())
	// Bind before announcing and before writing PID metadata: a port conflict
	// must fail loudly and must not leave a pid file behind, and a bind error
	// must never be followed by a misleading \"serving\" line
	// (docs/v1-scheme.md §14.2).
	if err := srv.Listen(addr); err != nil {
		fmt.Fprintf(stderr, "serve: %v\n", err)
		return ExitError
	}

	pidInfo := process.PIDFile{
		PID:       os.Getpid(),
		StartedAt: time.Now().UTC().Format(time.RFC3339),
		Listen:    addr,
		Version:   version.Version,
	}
	if err := process.WritePIDFile(pidPath, pidInfo); err != nil {
		fmt.Fprintf(stderr, "serve: cannot write pid file: %v\n", err)
		return ExitError
	}

	errCh := make(chan error, 1)
	go func() { errCh <- srv.Serve() }()

	fmt.Fprintf(stderr, "ai-gateway %s serving on http://%s (pid %d)\n",
		version.Version, addr, os.Getpid())

	reason := ""
	signals := process.NotifySignals()
loop:
	for {
		select {
		case <-signals:
			reason = "signal received"
			break loop
		case <-srv.ShutdownRequested():
			reason = "shutdown requested via management API"
			break loop
		case err := <-errCh:
			if err != nil {
				fmt.Fprintf(stderr, "serve: %v\n", err)
				return ExitError
			}
			reason = "server stopped"
			break loop
		}
	}
	fmt.Fprintf(stderr, "serve: %s, shutting down...\n", reason)

	ctx, cancel := context.WithTimeout(context.Background(), server.ShutdownGrace)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		fmt.Fprintf(stderr, "serve: graceful shutdown: %v\n", err)
		return ExitError
	}
	return ExitOK
}

// existingListen reports the listener address of a competing instance: the
// PID file's recorded address when available, otherwise the fallback.
func existingListen(pidPath, fallback string) string {
	info, err := process.ReadPIDFile(pidPath)
	if err == nil && info != nil && info.Listen != "" {
		return info.Listen
	}
	return fallback
}

// resolvePort returns the port to reach the gateway with. The live instance's
// recorded listener (pid.json) wins, then the configured port, then the
// default. This keeps stop/status correct when serve was started with --port
// or a non-default config.
func resolvePort() int {
	dataDir := DefaultDataDir()
	if info, err := process.ReadPIDFile(process.PIDPath(dataDir)); err == nil && info != nil {
		if p := portFromAddr(info.Listen); p > 0 {
			return p
		}
	}
	if cfg, err := config.NewManager(config.ConfigPath(dataDir)).Load(); err == nil {
		return cfg.Listen.PortValue()
	}
	return config.DefaultPort
}

// ManagementPort returns the loopback management port selected by the live
// PID metadata or persisted config. Desktop launchers use it only to discover
// the API address; gateway state and mutations still flow through HTTP.
func ManagementPort() int { return resolvePort() }

// portFromAddr extracts the port from a host:port address.
func portFromAddr(addr string) int {
	_, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		return 0
	}
	p, err := strconv.Atoi(portStr)
	if err != nil || p < 1 || p > 65535 {
		return 0
	}
	return p
}

func runStop(args []string) int {
	fs := flag.NewFlagSet("stop", flag.ContinueOnError)
	fs.SetOutput(stderr)
	if err := fs.Parse(args); err != nil {
		return ExitUsage
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(stderr, "stop: unexpected arguments: %v\n", fs.Args())
		return ExitUsage
	}
	port := resolvePort()
	ctx, cancel := context.WithTimeout(context.Background(), server.ShutdownGrace)
	defer cancel()
	client := server.NewAdminClient(port)
	if err := client.Shutdown(ctx); err != nil {
		fmt.Fprintf(stderr, "stop: ai-gateway is not running on 127.0.0.1:%d: %v\n", port, err)
		return ExitNotRunning
	}
	fmt.Fprintf(stdout, "ai-gateway shutdown requested on 127.0.0.1:%d\n", port)
	return ExitOK
}

func runStatus(args []string) int {
	fs := flag.NewFlagSet("status", flag.ContinueOnError)
	fs.SetOutput(stderr)
	if err := fs.Parse(args); err != nil {
		return ExitUsage
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(stderr, "status: unexpected arguments: %v\n", fs.Args())
		return ExitUsage
	}
	port := resolvePort()
	ctx, cancel := context.WithTimeout(context.Background(), server.ShutdownGrace)
	defer cancel()
	client := server.NewAdminClient(port)
	st, err := client.Status(ctx)
	if err == nil {
		data, merr := json.MarshalIndent(st, "", "  ")
		if merr != nil {
			fmt.Fprintf(stderr, "status: %v\n", merr)
			return ExitError
		}
		fmt.Fprintln(stdout, string(data))
		return ExitOK
	}

	// Unreachable: fall back to lock + PID diagnostics. The lock file itself
	// persists after a clean shutdown, so file existence is not an active
	// lock: only a probe result of LockHeld means a live instance holds it
	// (docs/v1-scheme.md §14.2).
	fmt.Fprintf(stderr, "status: ai-gateway is not running (management API unreachable on 127.0.0.1:%d): %v\n", port, err)
	dataDir := DefaultDataDir()
	info, rerr := process.ReadPIDFile(process.PIDPath(dataDir))
	if rerr == nil && info != nil {
		fmt.Fprintf(stderr, "status: stale pid file found (pid %d, listen %s, started %s)\n",
			info.PID, info.Listen, info.StartedAt)
	} else {
		fmt.Fprintln(stderr, "status: no pid file present")
	}
	switch state, lerr := process.ProbeLock(process.LockPath(dataDir)); {
	case lerr != nil:
		fmt.Fprintf(stderr, "status: cannot probe lock: %v\n", lerr)
	case state == process.LockHeld:
		fmt.Fprintln(stderr, "status: lock is held by a running gateway process, but the management API is unreachable")
	case state == process.LockFree:
		fmt.Fprintln(stderr, "status: lock file present but not held (stale); no gateway process is running")
	default:
		fmt.Fprintln(stderr, "status: no lock file present")
	}
	return ExitNotRunning
}

func runVersion(args []string) int {
	fs := flag.NewFlagSet("version", flag.ContinueOnError)
	fs.SetOutput(stderr)
	if err := fs.Parse(args); err != nil {
		return ExitUsage
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(stderr, "version: unexpected arguments: %v\n", fs.Args())
		return ExitUsage
	}
	fmt.Fprintln(stdout, version.String())
	return ExitOK
}

func runDoctor(args []string) int {
	fs := flag.NewFlagSet("doctor", flag.ContinueOnError)
	fs.SetOutput(stderr)
	if err := fs.Parse(args); err != nil {
		return ExitUsage
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(stderr, "doctor: unexpected arguments: %v\n", fs.Args())
		return ExitUsage
	}
	ctx, cancel := context.WithTimeout(context.Background(), server.ShutdownGrace)
	defer cancel()
	report, err := server.NewAdminClient(resolvePort()).Doctor(ctx)
	if err != nil {
		fmt.Fprintf(stderr, "doctor: gateway is not reachable: %v\n", err)
		return ExitNotRunning
	}
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		fmt.Fprintf(stderr, "doctor: encode report: %v\n", err)
		return ExitError
	}
	fmt.Fprintln(stdout, string(data))
	return ExitOK
}

func runAutostart(args []string) int {
	fs := flag.NewFlagSet("autostart", flag.ContinueOnError)
	fs.SetOutput(stderr)
	if err := fs.Parse(args); err != nil {
		return ExitUsage
	}
	if fs.NArg() != 1 || (fs.Arg(0) != "on" && fs.Arg(0) != "off") {
		fmt.Fprintln(stderr, "autostart: expected exactly one argument: on or off")
		return ExitUsage
	}
	enabled := fs.Arg(0) == "on"
	ctx, cancel := context.WithTimeout(context.Background(), server.ShutdownGrace)
	defer cancel()
	client := server.NewAdminClient(resolvePort())
	if err := client.Healthz(ctx); err == nil {
		result, err := client.SetAutostart(ctx, enabled)
		if err != nil {
			fmt.Fprintf(stderr, "autostart: gateway rejected the change: %v\n", err)
			return ExitError
		}
		fmt.Fprintf(stdout, "autostart %s (executable: %s)\n", fs.Arg(0), result.Executable)
		return ExitOK
	}

	dataDir := DefaultDataDir()
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		fmt.Fprintf(stderr, "autostart: cannot create data directory %s: %v\n", dataDir, err)
		return ExitError
	}
	manager := config.NewManager(config.ConfigPath(dataDir))
	if _, err := manager.LoadOrCreate(); err != nil {
		fmt.Fprintf(stderr, "autostart: %v\n", err)
		return ExitConfig
	}
	registration, err := autostart.Apply(autostart.NewCurrentExecutable(), manager, enabled)
	if err != nil {
		var applyErr *autostart.ApplyError
		if errors.As(err, &applyErr) && applyErr.Partial() {
			fmt.Fprintf(stderr, "autostart: partial failure: %v\n", err)
			return ExitPartial
		}
		fmt.Fprintf(stderr, "autostart: %v\n", err)
		return ExitError
	}
	fmt.Fprintf(stdout, "autostart %s (executable: %s)\n", fs.Arg(0), registration.Executable)
	return ExitOK
}
