package cache

import (
	"sync"
	"time"

	"github.com/demo/cache-server/internal/metrics"
)

// ─────────────────────────────────────────────────────────
// L1 — In-Process Cache (W-TinyLFU-inspired with sharding)
//
// Design goals:
//   - Sub-microsecond reads (no network, no syscalls)
//   - Lock contention minimized via sharding
//   - TTL-based expiry to limit staleness
//   - LRU eviction when shard is full
// ─────────────────────────────────────────────────────────

const numShards = 256

type l1Entry struct {
	value     []byte
	expiresAt time.Time
}

type l1Shard struct {
	mu       sync.RWMutex
	items    map[string]*l1Entry
	maxSize  int
	evictIdx int
	keys     []string // ring buffer for LRU-approximate eviction
}

// L1Cache is a sharded, TTL-aware in-process cache.
type L1Cache struct {
	shards [numShards]*l1Shard
	ttl    time.Duration
}

// NewL1 creates a new L1 in-process cache.
func NewL1(maxSize int, ttl time.Duration) *L1Cache {
	perShard := maxSize / numShards
	if perShard < 64 {
		perShard = 64
	}

	c := &L1Cache{ttl: ttl}
	for i := 0; i < numShards; i++ {
		c.shards[i] = &l1Shard{
			items:   make(map[string]*l1Entry, perShard),
			maxSize: perShard,
			keys:    make([]string, 0, perShard),
		}
	}
	return c
}

// getShard returns the shard for a given key using FNV-1a hash.
func (c *L1Cache) getShard(key string) *l1Shard {
	h := uint32(2166136261)
	for i := 0; i < len(key); i++ {
		h ^= uint32(key[i])
		h *= 16777619
	}
	return c.shards[h%numShards]
}

// Get retrieves a value from L1. Returns nil if not found or expired.
func (c *L1Cache) Get(key string) ([]byte, bool) {
	shard := c.getShard(key)
	shard.mu.RLock()
	entry, ok := shard.items[key]
	shard.mu.RUnlock()

	if !ok {
		metrics.Global.L1Misses.Add(1)
		return nil, false
	}

	// Check TTL expiry
	if time.Now().After(entry.expiresAt) {
		// Lazy delete
		shard.mu.Lock()
		delete(shard.items, key)
		shard.mu.Unlock()
		metrics.Global.L1Misses.Add(1)
		return nil, false
	}

	metrics.Global.L1Hits.Add(1)
	return entry.value, true
}

// Set stores a value in L1 with TTL.
func (c *L1Cache) Set(key string, value []byte) {
	shard := c.getShard(key)
	shard.mu.Lock()
	defer shard.mu.Unlock()

	// Evict if full (approximate LRU via ring buffer)
	if len(shard.items) >= shard.maxSize {
		if _, exists := shard.items[key]; !exists {
			c.evictOne(shard)
		}
	}

	entry := &l1Entry{
		value:     value,
		expiresAt: time.Now().Add(c.ttl),
	}

	if _, exists := shard.items[key]; !exists {
		shard.keys = append(shard.keys, key)
	}
	shard.items[key] = entry
}

// Delete removes a key from L1 (used for invalidation).
func (c *L1Cache) Delete(key string) {
	shard := c.getShard(key)
	shard.mu.Lock()
	delete(shard.items, key)
	shard.mu.Unlock()
}

// evictOne removes one item (approximate LRU). Must be called with lock held.
func (c *L1Cache) evictOne(shard *l1Shard) {
	for i := 0; i < len(shard.keys); i++ {
		idx := (shard.evictIdx + i) % len(shard.keys)
		key := shard.keys[idx]
		if _, ok := shard.items[key]; ok {
			delete(shard.items, key)
			shard.evictIdx = (idx + 1) % len(shard.keys)
			return
		}
	}
}

// Stats returns L1 size across all shards.
func (c *L1Cache) Stats() int {
	total := 0
	for _, shard := range c.shards {
		shard.mu.RLock()
		total += len(shard.items)
		shard.mu.RUnlock()
	}
	return total
}
