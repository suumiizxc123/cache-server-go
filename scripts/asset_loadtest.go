package main

import (
	"bytes"
	"flag"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// ═══════════════════════════════════════════════════════════
//  Binary Content Cache — Load Test
//
//  Tests the full binary content caching pipeline:
//    1. Upload all assets from sample-assets/ → Go server → MinIO
//    2. Fetch assets via Nginx cache (MISS → HIT cycle)
//    3. Measure throughput, hit rates, latency distribution
//
//  Usage:
//    go run scripts/asset_loadtest.go
//    go run scripts/asset_loadtest.go -c 100 -n 10000 -nginx http://localhost:80
// ═══════════════════════════════════════════════════════════

var (
	host      = flag.String("host", "localhost", "Target host/IP (e.g. 172.16.22.24)")
	uploadURL = flag.String("upload", "", "Go server upload endpoint (auto-set from -host)")
	useNginx  = flag.Bool("nginx", false, "Use Nginx cache (port 80) for reads")
	useHA     = flag.Bool("ha", false, "Use HAProxy → Nginx (port 80) for reads")
	assetDir  = flag.String("assets", "sample-assets", "Directory with test assets")
	numReqs   = flag.Int("n", 5000, "Total read requests")
	conc      = flag.Int("c", 50, "Concurrent workers")
	readOnly  = flag.Bool("read-only", false, "Skip upload, test reads only")
)

func readBaseURL() string {
	if *useHA {
		return fmt.Sprintf("http://%s:80", *host)
	}
	if *useNginx {
		return fmt.Sprintf("http://%s:8081", *host)
	}
	return fmt.Sprintf("http://%s:8080", *host)
}

func modeName() string {
	if *useHA {
		return fmt.Sprintf("HAProxy → Nginx (%s:80)", *host)
	}
	if *useNginx {
		return fmt.Sprintf("Nginx Direct (%s:8081)", *host)
	}
	return fmt.Sprintf("Go Cache Server (%s:8080)", *host)
}

type AssetInfo struct {
	Name string
	Size int64
	Type string
}

type Stats struct {
	totalReqs   int64
	totalBytes  int64
	cacheHits   int64
	cacheMisses int64
	errors      int64
	latencies   []time.Duration
	mu          sync.Mutex
}

func main() {
	flag.Parse()

	// Auto-set upload URL from host if not explicitly provided
	if *uploadURL == "" {
		*uploadURL = fmt.Sprintf("http://%s:8080", *host)
	}

	fmt.Println("═══════════════════════════════════════════")
	fmt.Println("  Binary Content Cache — Load Test")
	fmt.Println("═══════════════════════════════════════════")
	fmt.Printf("  Mode: %s\n", modeName())
	fmt.Println()

	// ── Phase 1: Discover assets ──
	assets := discoverAssets(*assetDir)
	if len(assets) == 0 {
		fmt.Println("ERROR: No assets found in", *assetDir)
		fmt.Println("Run: bash scripts/generate_assets.sh")
		os.Exit(1)
	}

	fmt.Printf("Found %d assets:\n", len(assets))
	var totalSize int64
	for _, a := range assets {
		fmt.Printf("  %-30s %10s  %s\n", a.Name, formatBytes(a.Size), a.Type)
		totalSize += a.Size
	}
	fmt.Printf("\nTotal asset size: %s\n\n", formatBytes(totalSize))

	// ── Phase 2: Upload all assets ──
	if !*readOnly {
		fmt.Println("── Phase 2: Uploading assets to MinIO ──")
		uploadAll(assets)
		fmt.Println()
	}

	// ── Phase 3: Warmup (fill Nginx cache) ──
	fmt.Println("── Phase 3: Cache warmup (filling Nginx cache) ──")
	warmupStats := warmupCache(assets)
	fmt.Printf("  Warmup: %d requests, %d MISS → cached\n\n",
		warmupStats.totalReqs, warmupStats.cacheMisses)

	// ── Phase 4: Load test (should be mostly HIT) ──
	fmt.Printf("── Phase 4: Load test (%d reqs, %d workers) ──\n", *numReqs, *conc)
	stats := runLoadTest(assets)

	// ── Results ──
	printResults(stats, assets)
}

func discoverAssets(dir string) []AssetInfo {
	var assets []AssetInfo
	entries, err := os.ReadDir(dir)
	if err != nil {
		return assets
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		info, _ := e.Info()
		assets = append(assets, AssetInfo{
			Name: e.Name(),
			Size: info.Size(),
			Type: detectMIME(e.Name()),
		})
	}
	// Sort by size descending
	sort.Slice(assets, func(i, j int) bool {
		return assets[i].Size > assets[j].Size
	})
	return assets
}

func uploadAll(assets []AssetInfo) {
	client := &http.Client{Timeout: 60 * time.Second}

	for _, a := range assets {
		data, err := os.ReadFile(filepath.Join(*assetDir, a.Name))
		if err != nil {
			fmt.Printf("  SKIP %s: %v\n", a.Name, err)
			continue
		}

		url := fmt.Sprintf("%s/upload/%s", *uploadURL, a.Name)
		req, _ := http.NewRequest("POST", url, bytes.NewReader(data))
		req.Header.Set("Content-Type", a.Type)

		resp, err := client.Do(req)
		if err != nil {
			fmt.Printf("  FAIL %s: %v\n", a.Name, err)
			continue
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()

		if resp.StatusCode >= 300 {
			fmt.Printf("  FAIL %s: HTTP %d — %s\n", a.Name, resp.StatusCode, string(body))
			continue
		}
		fmt.Printf("  ✓ %s (%s) → uploaded\n", a.Name, formatBytes(a.Size))
	}
}

func warmupCache(assets []AssetInfo) *Stats {
	stats := &Stats{}
	client := &http.Client{Timeout: 30 * time.Second}

	for _, a := range assets {
		url := fmt.Sprintf("%s/assets/%s", readBaseURL(), a.Name)
		start := time.Now()
		resp, err := client.Get(url)
		elapsed := time.Since(start)

		atomic.AddInt64(&stats.totalReqs, 1)

		if err != nil {
			atomic.AddInt64(&stats.errors, 1)
			fmt.Printf("  FAIL %s: %v\n", a.Name, err)
			continue
		}
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()

		cacheStatus := resp.Header.Get("X-Cache-Status")
		if cacheStatus == "HIT" {
			atomic.AddInt64(&stats.cacheHits, 1)
		} else {
			atomic.AddInt64(&stats.cacheMisses, 1)
		}

		fmt.Printf("  %s %-30s %s (%s)\n", cacheStatus, a.Name, formatBytes(a.Size), elapsed.Round(time.Millisecond))
	}
	return stats
}

func runLoadTest(assets []AssetInfo) *Stats {
	stats := &Stats{
		latencies: make([]time.Duration, 0, *numReqs),
	}

	client := &http.Client{
		Timeout: 30 * time.Second,
		Transport: &http.Transport{
			MaxIdleConns:        *conc * 2,
			MaxIdleConnsPerHost: *conc * 2,
			IdleConnTimeout:     90 * time.Second,
		},
	}

	var wg sync.WaitGroup
	reqCh := make(chan int, *numReqs)

	// Fill request channel
	for i := 0; i < *numReqs; i++ {
		reqCh <- i
	}
	close(reqCh)

	start := time.Now()

	// Progress reporter
	done := make(chan struct{})
	go func() {
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				total := atomic.LoadInt64(&stats.totalReqs)
				hits := atomic.LoadInt64(&stats.cacheHits)
				errs := atomic.LoadInt64(&stats.errors)
				elapsed := time.Since(start).Seconds()
				rps := float64(total) / elapsed
				hitRate := float64(0)
				if total > 0 {
					hitRate = float64(hits) / float64(total) * 100
				}
				fmt.Printf("\r  Progress: %d/%d reqs | %.0f RPS | %.1f%% HIT | %d errors    ",
					total, *numReqs, rps, hitRate, errs)
			case <-done:
				return
			}
		}
	}()

	// Launch workers
	for w := 0; w < *conc; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			rng := rand.New(rand.NewSource(time.Now().UnixNano()))

			for range reqCh {
				// Zipfian-like: favor smaller (more popular) assets
				idx := int(float64(len(assets)) * (float64(rng.Intn(100)) / 100.0) * (float64(rng.Intn(100)) / 100.0))
				if idx >= len(assets) {
					idx = len(assets) - 1
				}
				asset := assets[idx]

				url := fmt.Sprintf("%s/assets/%s", readBaseURL(), asset.Name)
				reqStart := time.Now()
				resp, err := client.Get(url)
				elapsed := time.Since(reqStart)

				atomic.AddInt64(&stats.totalReqs, 1)

				if err != nil {
					atomic.AddInt64(&stats.errors, 1)
					continue
				}

				n, _ := io.Copy(io.Discard, resp.Body)
				resp.Body.Close()
				atomic.AddInt64(&stats.totalBytes, n)

				cacheStatus := resp.Header.Get("X-Cache-Status")
				if cacheStatus == "HIT" {
					atomic.AddInt64(&stats.cacheHits, 1)
				} else {
					atomic.AddInt64(&stats.cacheMisses, 1)
				}

				stats.mu.Lock()
				stats.latencies = append(stats.latencies, elapsed)
				stats.mu.Unlock()
			}
		}()
	}

	wg.Wait()
	totalTime := time.Since(start)
	close(done)
	fmt.Println() // clear progress line

	// Attach total time for RPS calculation
	stats.mu.Lock()
	sort.Slice(stats.latencies, func(i, j int) bool {
		return stats.latencies[i] < stats.latencies[j]
	})
	stats.mu.Unlock()

	fmt.Printf("  Completed in %s\n\n", totalTime.Round(time.Millisecond))
	return stats
}

func printResults(stats *Stats, assets []AssetInfo) {
	total := atomic.LoadInt64(&stats.totalReqs)
	hits := atomic.LoadInt64(&stats.cacheHits)
	misses := atomic.LoadInt64(&stats.cacheMisses)
	errors := atomic.LoadInt64(&stats.errors)
	totalBytes := atomic.LoadInt64(&stats.totalBytes)

	hitRate := float64(0)
	if total > 0 {
		hitRate = float64(hits) / float64(total) * 100
	}

	fmt.Println("═══════════════════════════════════════════")
	fmt.Println("  RESULTS")
	fmt.Println("═══════════════════════════════════════════")
	fmt.Println()
	fmt.Printf("  Total requests:    %d\n", total)
	fmt.Printf("  Cache HITs:        %d (%.1f%%)\n", hits, hitRate)
	fmt.Printf("  Cache MISSes:      %d\n", misses)
	fmt.Printf("  Errors:            %d\n", errors)
	fmt.Printf("  Total transferred: %s\n", formatBytes(totalBytes))
	fmt.Println()

	if len(stats.latencies) > 0 {
		n := len(stats.latencies)
		// RPS from latency sum
		var latSum time.Duration
		for _, l := range stats.latencies {
			latSum += l
		}

		fmt.Println("  Latency Distribution:")
		fmt.Printf("    Min:    %s\n", stats.latencies[0].Round(time.Microsecond))
		fmt.Printf("    P50:    %s\n", stats.latencies[n*50/100].Round(time.Microsecond))
		fmt.Printf("    P90:    %s\n", stats.latencies[n*90/100].Round(time.Microsecond))
		fmt.Printf("    P95:    %s\n", stats.latencies[n*95/100].Round(time.Microsecond))
		fmt.Printf("    P99:    %s\n", stats.latencies[n*99/100].Round(time.Microsecond))
		fmt.Printf("    Max:    %s\n", stats.latencies[n-1].Round(time.Microsecond))
		fmt.Printf("    Avg:    %s\n", (latSum / time.Duration(n)).Round(time.Microsecond))
		fmt.Println()

		// Throughput
		if n > 1 {
			elapsed := stats.latencies[n-1] // approximate
			_ = elapsed
		}
	}

	fmt.Printf("  Throughput:  ~%s/sec data transferred\n", formatBytes(totalBytes/int64(max(1, len(stats.latencies)/(*conc)))))
	fmt.Println()

	// Per-asset breakdown (from warmup)
	fmt.Println("  Asset Summary:")
	for _, a := range assets {
		fmt.Printf("    %-30s %10s  %s\n", a.Name, formatBytes(a.Size), a.Type)
	}
	fmt.Println()
	fmt.Println("═══════════════════════════════════════════")
}

func formatBytes(b int64) string {
	switch {
	case b >= 1<<30:
		return fmt.Sprintf("%.1f GB", float64(b)/(1<<30))
	case b >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(b)/(1<<20))
	case b >= 1<<10:
		return fmt.Sprintf("%.1f KB", float64(b)/(1<<10))
	default:
		return fmt.Sprintf("%d B", b)
	}
}

func detectMIME(name string) string {
	ext := strings.ToLower(filepath.Ext(name))
	switch ext {
	case ".png":
		return "image/png"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".gif":
		return "image/gif"
	case ".webp":
		return "image/webp"
	case ".svg":
		return "image/svg+xml"
	case ".mp4":
		return "video/mp4"
	case ".ts":
		return "video/mp2t"
	case ".m3u8":
		return "application/vnd.apple.mpegurl"
	case ".webm":
		return "video/webm"
	case ".css":
		return "text/css"
	case ".js":
		return "application/javascript"
	case ".woff2":
		return "font/woff2"
	case ".woff":
		return "font/woff"
	default:
		return "application/octet-stream"
	}
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
