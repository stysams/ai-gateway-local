package server

import (
	"sync"
	"sync/atomic"
	"time"

	"ai-gateway/internal/config"
	"ai-gateway/internal/route"
)

// requestLimiter tracks active data-plane requests without waiting. A request
// that cannot acquire every applicable slot is rejected immediately.
type requestLimiter struct {
	mu        sync.Mutex
	global    int
	clients   map[route.ClientID]int
	providers map[string]int
	active    atomic.Int64
	rate      map[route.ClientID]rateWindow
}

type rateWindow struct {
	started time.Time
	count   int
}

func newRequestLimiter() *requestLimiter {
	return &requestLimiter{clients: make(map[route.ClientID]int), providers: make(map[string]int), rate: make(map[route.ClientID]rateWindow)}
}

// allowRate admits one request in a fixed one-minute window. A zero limit is
// disabled; requests over the limit are rejected without waiting.
func (l *requestLimiter) allowRate(limit int, client route.ClientID, now time.Time) bool {
	if limit <= 0 {
		return true
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	window := l.rate[client]
	if window.started.IsZero() || now.Sub(window.started) >= time.Minute {
		window = rateWindow{started: now}
	}
	if window.count >= limit {
		l.rate[client] = window
		return false
	}
	window.count++
	l.rate[client] = window
	return true
}

// tryAcquire atomically checks all applicable limits and increments every
// counter only when the request can enter. The returned release is idempotent.
func (l *requestLimiter) tryAcquire(limits config.Limits, client route.ClientID, provider string) (func(), bool) {
	l.mu.Lock()
	if (limits.Global > 0 && l.global >= limits.Global) ||
		(limits.PerClient > 0 && l.clients[client] >= limits.PerClient) ||
		(limits.PerProvider > 0 && l.providers[provider] >= limits.PerProvider) {
		l.mu.Unlock()
		return nil, false
	}
	l.global++
	l.clients[client]++
	l.providers[provider]++
	l.active.Add(1)
	l.mu.Unlock()

	var once sync.Once
	return func() {
		once.Do(func() {
			l.mu.Lock()
			l.global--
			l.clients[client]--
			l.providers[provider]--
			l.active.Add(-1)
			l.mu.Unlock()
		})
	}, true
}

func (l *requestLimiter) activeCount() int64 {
	if l == nil {
		return 0
	}
	return l.active.Load()
}
