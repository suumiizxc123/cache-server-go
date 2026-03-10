package cache

import (
	"fmt"
	"sync"
	"testing"
	"time"
)

func TestL1_BasicGetSet(t *testing.T) {
	c := NewL1(1000, 5*time.Second)

	// Miss
	_, ok := c.Get("missing")
	if ok {
		t.Fatal("expected miss for non-existent key")
	}

	// Set + Hit
	c.Set("key1", []byte("value1"))
	val, ok := c.Get("key1")
	if !ok {
		t.Fatal("expected hit after set")
	}
	if string(val) != "value1" {
		t.Fatalf("expected 'value1', got '%s'", string(val))
	}
}

func TestL1_TTLExpiry(t *testing.T) {
	c := NewL1(1000, 50*time.Millisecond) // 50ms TTL

	c.Set("expire-me", []byte("data"))

	// Should hit immediately
	_, ok := c.Get("expire-me")
	if !ok {
		t.Fatal("expected hit before TTL")
	}

	// Wait for expiry
	time.Sleep(60 * time.Millisecond)

	_, ok = c.Get("expire-me")
	if ok {
		t.Fatal("expected miss after TTL expiry")
	}
}

func TestL1_Eviction(t *testing.T) {
	maxSize := 512 // small cache
	c := NewL1(maxSize, 10*time.Second)

	// Fill beyond capacity
	for i := 0; i < maxSize*2; i++ {
		c.Set(fmt.Sprintf("key-%d", i), []byte("data"))
	}

	// Size should be capped (approximately)
	size := c.Stats()
	if size > maxSize+numShards { // allow some slack per shard
		t.Fatalf("expected size ~%d, got %d", maxSize, size)
	}
}

func TestL1_ConcurrentAccess(t *testing.T) {
	c := NewL1(10000, 5*time.Second)
	var wg sync.WaitGroup

	// Concurrent writes
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			for j := 0; j < 1000; j++ {
				key := fmt.Sprintf("key-%d-%d", n, j)
				c.Set(key, []byte("value"))
				c.Get(key)
			}
		}(i)
	}

	wg.Wait()

	if c.Stats() == 0 {
		t.Fatal("expected non-zero cache size after concurrent writes")
	}
}

func TestL1_Delete(t *testing.T) {
	c := NewL1(1000, 5*time.Second)

	c.Set("del-me", []byte("data"))
	_, ok := c.Get("del-me")
	if !ok {
		t.Fatal("expected hit before delete")
	}

	c.Delete("del-me")
	_, ok = c.Get("del-me")
	if ok {
		t.Fatal("expected miss after delete")
	}
}

// ── Benchmarks ──

func BenchmarkL1_Get_Hit(b *testing.B) {
	c := NewL1(100000, 30*time.Second)
	c.Set("bench-key", []byte(`{"data":"benchmark payload with realistic size for testing"}`))

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			c.Get("bench-key")
		}
	})
}

func BenchmarkL1_Get_Miss(b *testing.B) {
	c := NewL1(100000, 30*time.Second)

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			c.Get(fmt.Sprintf("miss-%d", i))
			i++
		}
	})
}

func BenchmarkL1_Set(b *testing.B) {
	c := NewL1(100000, 30*time.Second)
	payload := []byte(`{"data":"benchmark payload with realistic size for testing"}`)

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			c.Set(fmt.Sprintf("key-%d", i), payload)
			i++
		}
	})
}

func BenchmarkL1_Mixed_ReadHeavy(b *testing.B) {
	c := NewL1(100000, 30*time.Second)

	// Pre-populate
	for i := 0; i < 10000; i++ {
		c.Set(fmt.Sprintf("key-%d", i), []byte("data"))
	}

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			key := fmt.Sprintf("key-%d", i%10000)
			if i%10 == 0 { // 10% writes
				c.Set(key, []byte("updated"))
			} else { // 90% reads
				c.Get(key)
			}
			i++
		}
	})
}
