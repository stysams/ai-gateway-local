package server

import (
	"testing"
	"time"

	"ai-gateway/internal/config"
	"ai-gateway/internal/route"
)

func TestRequestLimiterAppliesGlobalClientAndProviderLimits(t *testing.T) {
	l := newRequestLimiter()
	limits := config.Limits{Global: 1, PerClient: 1, PerProvider: 2}
	release, ok := l.tryAcquire(limits, route.Codex, "provider-a")
	if !ok {
		t.Fatal("first request was rejected")
	}
	if _, ok := l.tryAcquire(limits, route.Codex, "provider-a"); ok {
		t.Fatal("same client exceeded per-client limit")
	}
	if _, ok := l.tryAcquire(limits, route.Claude, "provider-a"); ok {
		t.Fatal("global limit was not enforced")
	}
	release()
	releaseA, ok := l.tryAcquire(limits, route.Claude, "provider-a")
	if !ok {
		t.Fatal("second client was rejected after release")
	}
	releaseA()
	if got := l.activeCount(); got != 0 {
		t.Fatalf("active count=%d, want 0 after release", got)
	}
}

func TestRequestLimiterProviderLimitAndIdempotentRelease(t *testing.T) {
	l := newRequestLimiter()
	limits := config.Limits{PerProvider: 1}
	release, ok := l.tryAcquire(limits, route.Generic, "provider-a")
	if !ok {
		t.Fatal("first request was rejected")
	}
	if _, ok := l.tryAcquire(limits, route.Generic, "provider-a"); ok {
		t.Fatal("provider limit was not enforced")
	}
	release()
	release()
	if _, ok := l.tryAcquire(limits, route.Generic, "provider-a"); !ok {
		t.Fatal("provider slot was not released")
	}
}

func TestRequestLimiterRateWindow(t *testing.T) {
	l := newRequestLimiter()
	now := time.Unix(100, 0)
	if !l.allowRate(2, route.Generic, now) || !l.allowRate(2, route.Generic, now.Add(time.Second)) {
		t.Fatal("rate limiter rejected requests below limit")
	}
	if l.allowRate(2, route.Generic, now.Add(2*time.Second)) {
		t.Fatal("rate limiter accepted request over limit")
	}
	if !l.allowRate(2, route.Generic, now.Add(time.Minute)) {
		t.Fatal("rate limiter did not reset after one minute")
	}
}
