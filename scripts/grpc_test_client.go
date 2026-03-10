// ═══════════════════════════════════════════════════════
//  gRPC Test Client — Smoke test for cache service
//
//  Usage:  go run scripts/grpc_test_client.go [-addr localhost:9090]
//
//  Tests all gRPC endpoints using JSON codec (matching server).
// ═══════════════════════════════════════════════════════

package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/encoding"
)

// ── JSON Codec (must match server) ──

type jsonCodec struct{}

func (jsonCodec) Marshal(v interface{}) ([]byte, error)      { return json.Marshal(v) }
func (jsonCodec) Unmarshal(data []byte, v interface{}) error  { return json.Unmarshal(data, v) }
func (jsonCodec) Name() string                               { return "json" }

func init() { encoding.RegisterCodec(jsonCodec{}) }

// ── Message Types (mirrors server) ──

type GetReq struct{ Key string `json:"key"` }
type GetResp struct {
	Value     json.RawMessage `json:"value"`
	Source    int             `json:"source"`
	LatencyNs int64          `json:"latency_ns"`
}

type SetReq struct {
	Key   string `json:"key"`
	Value []byte `json:"value"`
}
type SetResp struct{ Success bool `json:"success"` }

type DelReq struct{ Key string `json:"key"` }
type DelResp struct{ Success bool `json:"success"` }

type StatsReq struct{}
type StatsResp struct {
	L1Hits      int64   `json:"l1_hits"`
	L1Misses    int64   `json:"l1_misses"`
	L1HitRate   float64 `json:"l1_hit_rate"`
	L2Hits      int64   `json:"l2_hits"`
	L2Misses    int64   `json:"l2_misses"`
	OriginCalls int64   `json:"origin_calls"`
	TotalReqs   int64   `json:"total_requests"`
	AvgLatUs    float64 `json:"avg_latency_us"`
	HitRate     float64 `json:"overall_hit_rate"`
}

type HealthReq struct{}
type HealthResp struct {
	Status    string `json:"status"`
	RedisConn bool   `json:"redis_connected"`
	Uptime    int64  `json:"uptime_seconds"`
}

type BatchGetReq struct{ Keys []string `json:"keys"` }
type BatchGetResp struct {
	Hits   int32 `json:"hits"`
	Misses int32 `json:"misses"`
}

func main() {
	addr := flag.String("addr", "localhost:9090", "gRPC server address")
	flag.Parse()

	fmt.Println("╔══════════════════════════════════════════╗")
	fmt.Println("║   gRPC Cache Server — Smoke Test         ║")
	fmt.Printf("║   Target: %s\n", *addr)
	fmt.Println("╚══════════════════════════════════════════╝")
	fmt.Println()

	// Connect with JSON codec
	conn, err := grpc.NewClient(*addr,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithDefaultCallOptions(grpc.CallContentSubtype("json")),
	)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to connect: %v\n", err)
		os.Exit(1)
	}
	defer conn.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	passed := 0
	failed := 0

	// ── Test 1: Health ──
	fmt.Print("  [1/6] Health .............. ")
	var healthResp HealthResp
	err = conn.Invoke(ctx, "/cache.v1.CacheService/Health", &HealthReq{}, &healthResp)
	if err != nil {
		fmt.Printf("FAIL: %v\n", err)
		failed++
	} else {
		fmt.Printf("OK  (status=%s, uptime=%ds, redis=%v)\n", healthResp.Status, healthResp.Uptime, healthResp.RedisConn)
		passed++
	}

	// ── Test 2: Set ──
	fmt.Print("  [2/6] Set key ............ ")
	var setResp SetResp
	err = conn.Invoke(ctx, "/cache.v1.CacheService/Set", &SetReq{
		Key:   "grpc-test-key",
		Value: []byte(`{"msg":"hello from gRPC","ts":1234567890}`),
	}, &setResp)
	if err != nil {
		fmt.Printf("FAIL: %v\n", err)
		failed++
	} else {
		fmt.Printf("OK  (success=%v)\n", setResp.Success)
		passed++
	}

	// ── Test 3: Get ──
	fmt.Print("  [3/6] Get key ............ ")
	var getResp GetResp
	err = conn.Invoke(ctx, "/cache.v1.CacheService/Get", &GetReq{Key: "grpc-test-key"}, &getResp)
	if err != nil {
		fmt.Printf("FAIL: %v\n", err)
		failed++
	} else {
		src := []string{"UNKNOWN", "L1", "L2", "ORIGIN"}[getResp.Source]
		fmt.Printf("OK  (source=%s, latency=%dμs, value=%s)\n", src, getResp.LatencyNs/1000, string(getResp.Value))
		passed++
	}

	// ── Test 4: Delete ──
	fmt.Print("  [4/6] Delete key ......... ")
	var delResp DelResp
	err = conn.Invoke(ctx, "/cache.v1.CacheService/Delete", &DelReq{Key: "grpc-test-key"}, &delResp)
	if err != nil {
		fmt.Printf("FAIL: %v\n", err)
		failed++
	} else {
		fmt.Printf("OK  (success=%v)\n", delResp.Success)
		passed++
	}

	// ── Test 5: BatchGet ──
	fmt.Print("  [5/6] BatchGet ........... ")
	var batchResp BatchGetResp
	err = conn.Invoke(ctx, "/cache.v1.CacheService/BatchGet", &BatchGetReq{
		Keys: []string{"product:1", "product:2", "product:3"},
	}, &batchResp)
	if err != nil {
		fmt.Printf("FAIL: %v\n", err)
		failed++
	} else {
		fmt.Printf("OK  (hits=%d, misses=%d)\n", batchResp.Hits, batchResp.Misses)
		passed++
	}

	// ── Test 6: Stats ──
	fmt.Print("  [6/6] Stats .............. ")
	var statsResp StatsResp
	err = conn.Invoke(ctx, "/cache.v1.CacheService/Stats", &StatsReq{}, &statsResp)
	if err != nil {
		fmt.Printf("FAIL: %v\n", err)
		failed++
	} else {
		fmt.Printf("OK  (reqs=%d, l1_rate=%.1f%%, origin=%d)\n",
			statsResp.TotalReqs, statsResp.L1HitRate*100, statsResp.OriginCalls)
		passed++
	}

	// ── Summary ──
	fmt.Println()
	fmt.Println("══════════════════════════════════════════")
	fmt.Printf("  Results: %d passed, %d failed\n", passed, failed)
	fmt.Println("══════════════════════════════════════════")

	if failed > 0 {
		os.Exit(1)
	}
}
