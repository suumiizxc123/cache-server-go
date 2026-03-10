package metrics

import (
	"sync/atomic"
	"time"
)

// Metrics tracks cache performance counters.
// Uses atomics for lock-free, high-throughput counting.
type Metrics struct {
	L1Hits   atomic.Int64
	L1Misses atomic.Int64
	L2Hits   atomic.Int64
	L2Misses atomic.Int64

	OriginCalls    atomic.Int64
	OriginErrors   atomic.Int64
	CoalescedCalls atomic.Int64

	TotalRequests atomic.Int64
	TotalLatencyNs atomic.Int64 // sum of all request latencies in nanoseconds
}

var Global = &Metrics{}

// Snapshot returns a point-in-time copy of all metrics.
type Snapshot struct {
	L1Hits         int64   `json:"l1_hits"`
	L1Misses       int64   `json:"l1_misses"`
	L1HitRate      float64 `json:"l1_hit_rate"`
	L2Hits         int64   `json:"l2_hits"`
	L2Misses       int64   `json:"l2_misses"`
	L2HitRate      float64 `json:"l2_hit_rate"`
	OriginCalls    int64   `json:"origin_calls"`
	OriginErrors   int64   `json:"origin_errors"`
	CoalescedCalls int64   `json:"coalesced_calls"`
	TotalRequests  int64   `json:"total_requests"`
	AvgLatencyUs   float64 `json:"avg_latency_us"`
	OverallHitRate float64 `json:"overall_hit_rate"`
}

func (m *Metrics) Snapshot() Snapshot {
	s := Snapshot{
		L1Hits:         m.L1Hits.Load(),
		L1Misses:       m.L1Misses.Load(),
		L2Hits:         m.L2Hits.Load(),
		L2Misses:       m.L2Misses.Load(),
		OriginCalls:    m.OriginCalls.Load(),
		OriginErrors:   m.OriginErrors.Load(),
		CoalescedCalls: m.CoalescedCalls.Load(),
		TotalRequests:  m.TotalRequests.Load(),
	}

	l1Total := s.L1Hits + s.L1Misses
	if l1Total > 0 {
		s.L1HitRate = float64(s.L1Hits) / float64(l1Total)
	}

	l2Total := s.L2Hits + s.L2Misses
	if l2Total > 0 {
		s.L2HitRate = float64(s.L2Hits) / float64(l2Total)
	}

	if s.TotalRequests > 0 {
		totalNs := m.TotalLatencyNs.Load()
		s.AvgLatencyUs = float64(totalNs) / float64(s.TotalRequests) / 1000.0
		s.OverallHitRate = float64(s.L1Hits+s.L2Hits) / float64(s.TotalRequests)
	}

	return s
}

// RecordLatency records the latency for a single request.
func (m *Metrics) RecordLatency(d time.Duration) {
	m.TotalLatencyNs.Add(int64(d))
	m.TotalRequests.Add(1)
}
