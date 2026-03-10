package cache

import (
	"context"
	"fmt"
	"time"

	"github.com/demo/cache-server/internal/metrics"
	"github.com/redis/go-redis/v9"
)

// ─────────────────────────────────────────────────────────
// L2 — Distributed Cache (Redis)
//
// Design goals:
//   - Connection pooling for high concurrency
//   - Pipeline support for batch operations
//   - Cluster-aware client (auto-sharding)
//   - Graceful fallback on Redis failures
// ─────────────────────────────────────────────────────────

type L2Cache struct {
	client redis.UniversalClient
	ttl    time.Duration
}

// NewL2 creates a new Redis-backed L2 cache.
// Supports both single-node and cluster modes.
func NewL2(addrs []string, password string, poolSize int, ttl time.Duration) (*L2Cache, error) {
	var client redis.UniversalClient

	if len(addrs) > 1 {
		// Cluster mode
		client = redis.NewClusterClient(&redis.ClusterOptions{
			Addrs:        addrs,
			Password:     password,
			PoolSize:     poolSize,
			MinIdleConns: poolSize / 4,
			ReadOnly:     true, // read from replicas
			RouteByLatency: true,

			// Timeouts tuned for low-latency
			DialTimeout:  2 * time.Second,
			ReadTimeout:  500 * time.Millisecond,
			WriteTimeout: 500 * time.Millisecond,
			PoolTimeout:  1 * time.Second,
		})
	} else {
		// Single node (dev / demo mode)
		client = redis.NewClient(&redis.Options{
			Addr:         addrs[0],
			Password:     password,
			PoolSize:     poolSize,
			MinIdleConns: poolSize / 4,

			DialTimeout:  2 * time.Second,
			ReadTimeout:  500 * time.Millisecond,
			WriteTimeout: 500 * time.Millisecond,
			PoolTimeout:  1 * time.Second,
		})
	}

	// Verify connectivity
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	if err := client.Ping(ctx).Err(); err != nil {
		return nil, fmt.Errorf("redis ping failed: %w", err)
	}

	return &L2Cache{client: client, ttl: ttl}, nil
}

// Get retrieves a value from Redis.
func (c *L2Cache) Get(ctx context.Context, key string) ([]byte, bool) {
	val, err := c.client.Get(ctx, key).Bytes()
	if err != nil {
		if err != redis.Nil {
			// Real error (network, timeout, etc.)
			metrics.Global.OriginErrors.Add(1)
		}
		metrics.Global.L2Misses.Add(1)
		return nil, false
	}

	metrics.Global.L2Hits.Add(1)
	return val, true
}

// Set stores a value in Redis with TTL.
func (c *L2Cache) Set(ctx context.Context, key string, value []byte) error {
	return c.client.Set(ctx, key, value, c.ttl).Err()
}

// Delete removes a key from Redis (invalidation).
func (c *L2Cache) Delete(ctx context.Context, key string) error {
	return c.client.Del(ctx, key).Err()
}

// MGet retrieves multiple keys in a single round-trip (pipelining).
func (c *L2Cache) MGet(ctx context.Context, keys []string) (map[string][]byte, error) {
	results := make(map[string][]byte, len(keys))

	vals, err := c.client.MGet(ctx, keys...).Result()
	if err != nil {
		return nil, err
	}

	for i, val := range vals {
		if val != nil {
			if str, ok := val.(string); ok {
				results[keys[i]] = []byte(str)
				metrics.Global.L2Hits.Add(1)
			}
		} else {
			metrics.Global.L2Misses.Add(1)
		}
	}

	return results, nil
}

// Pipeline executes multiple SET operations in a single round-trip.
func (c *L2Cache) MSet(ctx context.Context, items map[string][]byte) error {
	pipe := c.client.Pipeline()
	for k, v := range items {
		pipe.Set(ctx, k, v, c.ttl)
	}
	_, err := pipe.Exec(ctx)
	return err
}

// Client returns the underlying Redis client (for use by other handlers).
func (c *L2Cache) Client() redis.UniversalClient {
	return c.client
}

// PoolStats returns connection pool statistics.
func (c *L2Cache) PoolStats() *redis.PoolStats {
	return c.client.PoolStats()
}

// Close gracefully shuts down the Redis client.
func (c *L2Cache) Close() error {
	return c.client.Close()
}
