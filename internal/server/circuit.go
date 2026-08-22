package server

import (
	"errors"
	"sync"
	"time"
)

var errProviderCircuitOpen = errors.New("provider circuit is open")

const (
	circuitFailureThreshold = 3
	circuitCooldown         = 30 * time.Second
)

type providerCircuit struct {
	failures int
	openedAt time.Time
}

type circuitBreaker struct {
	mu     sync.Mutex
	states map[string]providerCircuit
}

type providerCircuitHealth struct {
	Failures int  `json:"failures"`
	Open     bool `json:"open"`
}

func newCircuitBreaker() *circuitBreaker {
	return &circuitBreaker{states: make(map[string]providerCircuit)}
}

func (b *circuitBreaker) allow(provider string, now time.Time) bool {
	if b == nil {
		return true
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	state := b.states[provider]
	if state.openedAt.IsZero() {
		return true
	}
	if now.Sub(state.openedAt) < circuitCooldown {
		return false
	}
	// Half-open: permit one request and clear the failure count. A failed
	// probe will open the circuit again through observe.
	delete(b.states, provider)
	return true
}

func (b *circuitBreaker) observe(provider string, failed bool, now time.Time) {
	if b == nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if !failed {
		delete(b.states, provider)
		return
	}
	state := b.states[provider]
	if !state.openedAt.IsZero() {
		return
	}
	state.failures++
	if state.failures >= circuitFailureThreshold {
		state.openedAt = now
	}
	b.states[provider] = state
}

func (b *circuitBreaker) snapshot(now time.Time) map[string]providerCircuitHealth {
	if b == nil {
		return nil
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make(map[string]providerCircuitHealth, len(b.states))
	for provider, state := range b.states {
		open := !state.openedAt.IsZero() && now.Sub(state.openedAt) < circuitCooldown
		out[provider] = providerCircuitHealth{Failures: state.failures, Open: open}
	}
	return out
}
