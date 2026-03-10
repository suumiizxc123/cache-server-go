package origin

import (
	"context"
	"fmt"
	"math/rand"
	"time"
)

// ─────────────────────────────────────────────────────────
// Simulated Origin (Database / API Backend)
//
// In production, this would be your PostgreSQL, MySQL,
// or upstream API service. For the demo, we simulate
// realistic latency and generate sample payloads.
// ─────────────────────────────────────────────────────────

type SimulatedOrigin struct {
	baseLatency time.Duration
	jitter      time.Duration
}

func NewSimulated(baseLatency time.Duration) *SimulatedOrigin {
	return &SimulatedOrigin{
		baseLatency: baseLatency,
		jitter:      baseLatency / 4, // ±25% jitter
	}
}

// Fetch simulates a database lookup with realistic latency.
func (o *SimulatedOrigin) Fetch(ctx context.Context, key string) ([]byte, error) {
	// Simulate variable latency
	delay := o.baseLatency + time.Duration(rand.Int63n(int64(o.jitter)))

	select {
	case <-time.After(delay):
		// Generate a realistic-looking payload
		payload := fmt.Sprintf(
			`{"key":"%s","data":"payload_%s_%d","timestamp":%d,"source":"origin","ttl":300}`,
			key, key, rand.Intn(1000000), time.Now().UnixMilli(),
		)
		return []byte(payload), nil

	case <-ctx.Done():
		return nil, ctx.Err()
	}
}
