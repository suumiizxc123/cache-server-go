package cache

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/demo/cache-server/internal/metrics"
	"golang.org/x/sync/singleflight"
)

// ─────────────────────────────────────────────────────────
// CacheManager — Two-Tier Cache Orchestrator
//
// Implements the cache-aside pattern:
//   1. Check L1 (in-process, ~50μs)
//   2. On miss → check L2 (Redis, ~0.5ms)
//   3. On miss → call origin (DB/API, ~5-50ms)
//   4. Back-populate both layers
//
// Singleflight prevents thundering herd on cache misses.
// ─────────────────────────────────────────────────────────

// OriginFetcher is the function signature for fetching from the origin.
type OriginFetcher func(ctx context.Context, key string) ([]byte, error)

type Manager struct {
	l1     *L1Cache
	l2     *L2Cache
	origin OriginFetcher
	group  singleflight.Group
	useSF  bool
	logger *slog.Logger
}

// NewManager creates a new two-tier cache manager.
func NewManager(l1 *L1Cache, l2 *L2Cache, origin OriginFetcher, useSingleflight bool) *Manager {
	return &Manager{
		l1:     l1,
		l2:     l2,
		origin: origin,
		useSF:  useSingleflight,
		logger: slog.Default(),
	}
}

// Get retrieves a value using the two-tier cache-aside pattern.
func (m *Manager) Get(ctx context.Context, key string) ([]byte, error) {
	start := time.Now()
	defer func() {
		metrics.Global.RecordLatency(time.Since(start))
	}()

	// ── L1: In-Process Cache ──
	if val, ok := m.l1.Get(key); ok {
		return val, nil
	}

	// ── L2: Redis ──
	if val, ok := m.l2.Get(ctx, key); ok {
		// Back-populate L1
		m.l1.Set(key, val)
		return val, nil
	}

	// ── Origin: with Singleflight ──
	val, err := m.fetchFromOrigin(ctx, key)
	if err != nil {
		return nil, err
	}

	return val, nil
}

// fetchFromOrigin fetches from origin with optional singleflight coalescing.
func (m *Manager) fetchFromOrigin(ctx context.Context, key string) ([]byte, error) {
	if !m.useSF {
		return m.doOriginFetch(ctx, key)
	}

	// Singleflight: coalesce concurrent requests for the same key
	v, err, shared := m.group.Do(key, func() (interface{}, error) {
		return m.doOriginFetch(ctx, key)
	})

	if shared {
		metrics.Global.CoalescedCalls.Add(1)
	}

	if err != nil {
		return nil, err
	}

	return v.([]byte), nil
}

// doOriginFetch actually calls the origin and back-populates caches.
func (m *Manager) doOriginFetch(ctx context.Context, key string) ([]byte, error) {
	metrics.Global.OriginCalls.Add(1)

	val, err := m.origin(ctx, key)
	if err != nil {
		metrics.Global.OriginErrors.Add(1)
		return nil, fmt.Errorf("origin fetch failed for key %q: %w", key, err)
	}

	// Back-populate L2 (async, fire-and-forget)
	go func() {
		bgCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if err := m.l2.Set(bgCtx, key, val); err != nil {
			m.logger.Warn("L2 back-populate failed", "key", key, "error", err)
		}
	}()

	// Back-populate L1 (synchronous, in-process)
	m.l1.Set(key, val)

	return val, nil
}

// Set writes a value to both cache layers (write-through).
func (m *Manager) Set(ctx context.Context, key string, value []byte) error {
	m.l1.Set(key, value)
	return m.l2.Set(ctx, key, value)
}

// Invalidate removes a key from both cache layers.
func (m *Manager) Invalidate(ctx context.Context, key string) error {
	m.l1.Delete(key)
	return m.l2.Delete(ctx, key)
}

// Stats returns cache statistics.
type Stats struct {
	L1Size     int              `json:"l1_size"`
	L2PoolHits uint32           `json:"l2_pool_hits"`
	L2PoolMiss uint32           `json:"l2_pool_misses"`
	L2Conns    uint32           `json:"l2_total_conns"`
	Metrics    metrics.Snapshot `json:"metrics"`
}

func (m *Manager) Stats() Stats {
	poolStats := m.l2.PoolStats()
	return Stats{
		L1Size:     m.l1.Stats(),
		L2PoolHits: poolStats.Hits,
		L2PoolMiss: poolStats.Misses,
		L2Conns:    poolStats.TotalConns,
		Metrics:    metrics.Global.Snapshot(),
	}
}
