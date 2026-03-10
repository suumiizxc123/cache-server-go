// ═══════════════════════════════════════════════════════
//  Load Test — Concurrent cache performance benchmark
//
//  Usage:
//    go run scripts/loadtest.go [flags]
//
//  Flags:
//    -url       Base URL (default: http://localhost:8080)
//    -n         Total requests (default: 100000)
//    -c         Concurrent workers (default: 200)
//    -keys      Key space size (default: 10000)
//    -duration  Duration-based test (default: 0, uses -n instead)
// ═══════════════════════════════════════════════════════

package main

import (
	"flag"
	"fmt"
	"io"
	"math"
	"math/rand"
	"net/http"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

func main() {
	baseURL := flag.String("url", "http://localhost:8080", "base URL")
	totalReqs := flag.Int("n", 100000, "total requests")
	concurrency := flag.Int("c", 200, "concurrent workers")
	keySpace := flag.Int("keys", 10000, "key space size (Zipfian distribution)")
	duration := flag.Duration("duration", 0, "run for duration instead of count")
	flag.Parse()

	fmt.Println("╔══════════════════════════════════════════════╗")
	fmt.Println("║   Cache Server Load Test                     ║")
	fmt.Println("╠══════════════════════════════════════════════╣")
	fmt.Printf("║  URL:         %s\n", *baseURL)
	fmt.Printf("║  Requests:    %d\n", *totalReqs)
	fmt.Printf("║  Concurrency: %d\n", *concurrency)
	fmt.Printf("║  Key Space:   %d (Zipfian)\n", *keySpace)
	fmt.Println("╚══════════════════════════════════════════════╝")
	fmt.Println()

	// Warm up: populate some keys first
	fmt.Print("Warming up (populating 1000 keys)... ")
	warmUp(*baseURL, 1000)
	fmt.Println("done")

	// Setup HTTP client with connection pooling
	client := &http.Client{
		Transport: &http.Transport{
			MaxIdleConnsPerHost: *concurrency,
			MaxIdleConns:        *concurrency * 2,
			IdleConnTimeout:     90 * time.Second,
			DisableCompression:  true,
		},
		Timeout: 5 * time.Second,
	}

	var (
		completed atomic.Int64
		errors    atomic.Int64
		latencies []time.Duration
		mu        sync.Mutex
		wg        sync.WaitGroup
	)

	latencies = make([]time.Duration, 0, *totalReqs)
	reqCh := make(chan int, *concurrency*2)
	start := time.Now()

	// Workers
	for i := 0; i < *concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for keyIdx := range reqCh {
				// Zipfian-distributed key (hot keys get more hits)
				key := fmt.Sprintf("product:%d", keyIdx)
				url := fmt.Sprintf("%s/cache/%s", *baseURL, key)

				reqStart := time.Now()
				resp, err := client.Get(url)
				elapsed := time.Since(reqStart)

				if err != nil {
					errors.Add(1)
					continue
				}
				io.Copy(io.Discard, resp.Body)
				resp.Body.Close()

				if resp.StatusCode != 200 {
					errors.Add(1)
					continue
				}

				completed.Add(1)
				mu.Lock()
				latencies = append(latencies, elapsed)
				mu.Unlock()
			}
		}()
	}

	// Producer: generate requests with Zipfian distribution
	if *duration > 0 {
		// Duration-based
		deadline := time.After(*duration)
		go func() {
			z := newZipfian(*keySpace)
			for {
				select {
				case <-deadline:
					close(reqCh)
					return
				default:
					reqCh <- z.next()
				}
			}
		}()
	} else {
		// Count-based
		go func() {
			z := newZipfian(*keySpace)
			for i := 0; i < *totalReqs; i++ {
				reqCh <- z.next()
			}
			close(reqCh)
		}()
	}

	// Progress reporter
	go func() {
		ticker := time.NewTicker(2 * time.Second)
		defer ticker.Stop()
		for range ticker.C {
			c := completed.Load()
			e := errors.Load()
			elapsed := time.Since(start).Seconds()
			rps := float64(c) / elapsed
			fmt.Printf("  [%.1fs] completed: %d | errors: %d | RPS: %.0f\n",
				elapsed, c, e, rps)
		}
	}()

	wg.Wait()
	totalElapsed := time.Since(start)

	// ── Results ──
	sort.Slice(latencies, func(i, j int) bool { return latencies[i] < latencies[j] })

	total := completed.Load()
	errs := errors.Load()
	rps := float64(total) / totalElapsed.Seconds()

	fmt.Println()
	fmt.Println("═══════════════════════════════════════════════")
	fmt.Println("  RESULTS")
	fmt.Println("═══════════════════════════════════════════════")
	fmt.Printf("  Total requests:  %d\n", total+errs)
	fmt.Printf("  Successful:      %d\n", total)
	fmt.Printf("  Errors:          %d (%.2f%%)\n", errs, float64(errs)/float64(total+errs)*100)
	fmt.Printf("  Duration:        %s\n", totalElapsed.Round(time.Millisecond))
	fmt.Printf("  Throughput:      %.0f req/s\n", rps)
	fmt.Println()

	if len(latencies) > 0 {
		fmt.Println("  Latency Distribution:")
		fmt.Printf("    p50:  %s\n", percentile(latencies, 50))
		fmt.Printf("    p90:  %s\n", percentile(latencies, 90))
		fmt.Printf("    p95:  %s\n", percentile(latencies, 95))
		fmt.Printf("    p99:  %s\n", percentile(latencies, 99))
		fmt.Printf("    p999: %s\n", percentile(latencies, 99.9))
		fmt.Printf("    max:  %s\n", latencies[len(latencies)-1])
		fmt.Printf("    avg:  %s\n", avg(latencies))
	}
	fmt.Println("═══════════════════════════════════════════════")

	// Print server stats
	fmt.Println()
	fmt.Print("Fetching server stats... ")
	resp, err := client.Get(*baseURL + "/stats")
	if err == nil {
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		fmt.Println("done")
		fmt.Println(string(body))
	} else {
		fmt.Println("failed:", err)
	}
}

func warmUp(baseURL string, n int) {
	client := &http.Client{Timeout: 5 * time.Second}
	for i := 0; i < n; i++ {
		key := fmt.Sprintf("product:%d", i)
		url := fmt.Sprintf("%s/cache/%s", baseURL, key)
		resp, err := client.Get(url)
		if err == nil {
			io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
		}
	}
}

func percentile(sorted []time.Duration, p float64) time.Duration {
	idx := int(math.Ceil(p/100*float64(len(sorted)))) - 1
	if idx < 0 {
		idx = 0
	}
	if idx >= len(sorted) {
		idx = len(sorted) - 1
	}
	return sorted[idx]
}

func avg(durations []time.Duration) time.Duration {
	if len(durations) == 0 {
		return 0
	}
	var sum time.Duration
	for _, d := range durations {
		sum += d
	}
	return sum / time.Duration(len(durations))
}

// ── Zipfian Distribution ──
// Simulates real-world cache access patterns where a small
// number of keys receive the majority of requests.

type zipfian struct {
	n    int
	zipf *rand.Zipf
}

func newZipfian(n int) *zipfian {
	src := rand.NewSource(time.Now().UnixNano())
	r := rand.New(src)
	z := rand.NewZipf(r, 1.1, 1.0, uint64(n-1))
	return &zipfian{n: n, zipf: z}
}

func (z *zipfian) next() int {
	return int(z.zipf.Uint64())
}
