package server

import (
	"context"
	"sync/atomic"
	"time"
)

var metricDurationBuckets = [...]time.Duration{
	10 * time.Millisecond, 25 * time.Millisecond, 50 * time.Millisecond,
	100 * time.Millisecond, 250 * time.Millisecond, 500 * time.Millisecond,
	1 * time.Second, 5 * time.Second,
}

type gatewayMetrics struct {
	requests      atomic.Uint64
	success       atomic.Uint64
	failed        atomic.Uint64
	cancelled     atomic.Uint64
	upstreamError atomic.Uint64
	durations     [9]atomic.Uint64
	firstBytes    [9]atomic.Uint64
}

type MetricsResponse struct {
	Requests      uint64                           `json:"requests"`
	Success       uint64                           `json:"success"`
	Failed        uint64                           `json:"failed"`
	Cancelled     uint64                           `json:"cancelled"`
	UpstreamError uint64                           `json:"upstream_errors"`
	Active        int64                            `json:"active_requests"`
	LogSessions   int                              `json:"active_log_sessions"`
	Latency       MetricPercentile                 `json:"latency_ms"`
	FirstByte     MetricPercentile                 `json:"first_byte_ms"`
	Providers     map[string]providerCircuitHealth `json:"providers"`
}

type MetricPercentile struct {
	P50 float64 `json:"p50"`
	P95 float64 `json:"p95"`
}

func newGatewayMetrics() *gatewayMetrics { return &gatewayMetrics{} }

func (m *gatewayMetrics) observe(ctx context.Context, started time.Time, firstByte time.Duration, status int) {
	if m == nil {
		return
	}
	m.requests.Add(1)
	switch {
	case ctx != nil && ctx.Err() != nil:
		m.cancelled.Add(1)
	case status >= 400:
		m.failed.Add(1)
	default:
		m.success.Add(1)
	}
	if status >= 502 {
		m.upstreamError.Add(1)
	}
	addDuration(&m.durations, time.Since(started))
	if firstByte > 0 {
		addDuration(&m.firstBytes, firstByte)
	}
}

func addDuration(hist *[9]atomic.Uint64, duration time.Duration) {
	for i, bucket := range metricDurationBuckets {
		if duration <= bucket {
			hist[i].Add(1)
			return
		}
	}
	hist[len(hist)-1].Add(1)
}

func percentile(hist *[9]atomic.Uint64, total uint64, ratio float64) float64 {
	if total == 0 {
		return 0
	}
	target := uint64(float64(total-1)*ratio) + 1
	var seen uint64
	for i := range hist {
		seen += hist[i].Load()
		if seen >= target {
			if i == len(metricDurationBuckets) {
				return metricDurationBuckets[len(metricDurationBuckets)-1].Seconds() * 1000
			}
			return metricDurationBuckets[i].Seconds() * 1000
		}
	}
	return metricDurationBuckets[len(metricDurationBuckets)-1].Seconds() * 1000
}

func (s *Server) metricsSnapshot() MetricsResponse {
	m := s.metrics
	if m == nil {
		return MetricsResponse{}
	}
	total := m.requests.Load()
	firstTotal := uint64(0)
	for i := range m.firstBytes {
		firstTotal += m.firstBytes[i].Load()
	}
	return MetricsResponse{
		Requests: total, Success: m.success.Load(), Failed: m.failed.Load(),
		Cancelled: m.cancelled.Load(), UpstreamError: m.upstreamError.Load(),
		Active: s.limiter.activeCount(), LogSessions: s.warnings.ActiveSessions(),
		Latency:   MetricPercentile{P50: percentile(&m.durations, total, .50), P95: percentile(&m.durations, total, .95)},
		FirstByte: MetricPercentile{P50: percentile(&m.firstBytes, firstTotal, .50), P95: percentile(&m.firstBytes, firstTotal, .95)},
		Providers: s.circuits.snapshot(time.Now()),
	}
}
