package main

import (
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"sync"
	"time"

	"ai-gateway/internal/app"
	"ai-gateway/internal/process"
)

const gatewayReadyTimeout = 10 * time.Second

// ensureMu serializes start/stop-adjacent launches inside one desktop
// process so two tray clicks cannot both decide to spawn serve.
var (
	ensureMu     sync.Mutex
	readyTimeout = gatewayReadyTimeout
	startServe   = startDetachedServe
)

type gatewayLaunchAction int

const (
	gatewayAlreadyReady gatewayLaunchAction = iota
	gatewayWaitForExisting
	gatewaySpawnServe
)

// decideGatewayLaunch chooses whether the desktop should attach to a live
// gateway, wait for one that already holds gateway.lock, or start serve.
// A held lock is the only liveness authority (docs/v1-scheme.md §14.2);
// spawning a second serve while the lock is held produces a short-lived
// extra process that immediately exits.
func decideGatewayLaunch(reachable bool, lock process.LockState) gatewayLaunchAction {
	if reachable {
		return gatewayAlreadyReady
	}
	if lock == process.LockHeld {
		return gatewayWaitForExisting
	}
	return gatewaySpawnServe
}

func ensureGateway(port int) error {
	ensureMu.Lock()
	defer ensureMu.Unlock()

	url := fmt.Sprintf("http://127.0.0.1:%d/healthz", port)
	reachable := gatewayReachable(url)
	state, err := process.ProbeLock(process.LockPath(app.DefaultDataDir()))
	if err != nil {
		state = process.LockAbsent
	}
	switch decideGatewayLaunch(reachable, state) {
	case gatewayAlreadyReady:
		return nil
	case gatewayWaitForExisting:
		return waitGatewayReady(url, readyTimeout)
	default:
		if err := startServe(); err != nil {
			return err
		}
		return waitGatewayReady(url, readyTimeout)
	}
}

func startDetachedServe() error {
	executable, err := os.Executable()
	if err != nil {
		return fmt.Errorf("locate desktop executable: %w", err)
	}
	cmd := exec.Command(executable, "serve")
	prepareDetached(cmd)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start gateway process: %w", err)
	}
	_ = cmd.Process.Release()
	return nil
}

func waitGatewayReady(url string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if gatewayReachable(url) {
			return nil
		}
		time.Sleep(150 * time.Millisecond)
	}
	return fmt.Errorf("management API did not become ready at %s", url)
}

func gatewayReachable(url string) bool {
	client := &http.Client{Timeout: 700 * time.Millisecond}
	resp, err := client.Get(url)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}
