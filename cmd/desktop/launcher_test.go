package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"ai-gateway/internal/process"
)

func TestEnsureGatewayDoesNotSpawnWhenLockHeld(t *testing.T) {
	dataDir := t.TempDir()
	t.Setenv("AI_GATEWAY_DATA_DIR", dataDir)
	lock, err := process.AcquireLock(process.LockPath(dataDir))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = lock.Release() })

	spawned := 0
	origStart, origTimeout := startServe, readyTimeout
	startServe = func() error { spawned++; return nil }
	readyTimeout = 80 * time.Millisecond
	t.Cleanup(func() {
		startServe = origStart
		readyTimeout = origTimeout
	})

	if err := ensureGateway(1); err == nil {
		t.Fatal("ensureGateway succeeded, want wait timeout")
	}
	if spawned != 0 {
		t.Fatalf("spawned serve %d times, want 0 while gateway.lock is held", spawned)
	}
}

func TestEnsureGatewaySpawnsWhenLockFree(t *testing.T) {
	dataDir := t.TempDir()
	t.Setenv("AI_GATEWAY_DATA_DIR", dataDir)

	spawned := 0
	origStart, origTimeout := startServe, readyTimeout
	startServe = func() error { spawned++; return nil }
	readyTimeout = 80 * time.Millisecond
	t.Cleanup(func() {
		startServe = origStart
		readyTimeout = origTimeout
	})

	if err := ensureGateway(1); err == nil {
		t.Fatal("ensureGateway succeeded, want wait timeout after spawn")
	}
	if spawned != 1 {
		t.Fatalf("spawned serve %d times, want 1 when gateway.lock is free", spawned)
	}
}

func TestDecideGatewayLaunch(t *testing.T) {
	cases := []struct {
		name      string
		reachable bool
		lock      process.LockState
		want      gatewayLaunchAction
	}{
		{"ready ignores lock", true, process.LockHeld, gatewayAlreadyReady},
		{"ready when lock free", true, process.LockFree, gatewayAlreadyReady},
		{"wait when lock held and not ready", false, process.LockHeld, gatewayWaitForExisting},
		{"spawn when lock free", false, process.LockFree, gatewaySpawnServe},
		{"spawn when lock absent", false, process.LockAbsent, gatewaySpawnServe},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := decideGatewayLaunch(tc.reachable, tc.lock)
			if got != tc.want {
				t.Fatalf("decideGatewayLaunch(%v, %v) = %v, want %v", tc.reachable, tc.lock, got, tc.want)
			}
		})
	}
}

func TestWaitGatewayReady(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)
	if err := waitGatewayReady(srv.URL+"/healthz", time.Second); err != nil {
		t.Fatalf("waitGatewayReady: %v", err)
	}
}

func TestWaitGatewayReadyTimeout(t *testing.T) {
	err := waitGatewayReady("http://127.0.0.1:1/healthz", 80*time.Millisecond)
	if err == nil {
		t.Fatal("waitGatewayReady succeeded, want timeout")
	}
}
