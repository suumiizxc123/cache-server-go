// Package cachepb contains hand-written Go structs mirroring the protobuf
// definitions. In production, you would run `protoc` to auto-generate these.
//
// For this MVP demo, we use manual structs so no protoc toolchain is needed.
// When you're ready for production:
//
//   1. Install protoc + protoc-gen-go + protoc-gen-go-grpc
//   2. Run: protoc --go_out=. --go-grpc_out=. api/proto/cache.proto
//   3. Replace this file with the generated code
//
package cachepb

// ── Enums ──

type CacheHitSource int32

const (
	CacheHitSource_UNSPECIFIED CacheHitSource = 0
	CacheHitSource_L1          CacheHitSource = 1
	CacheHitSource_L2          CacheHitSource = 2
	CacheHitSource_ORIGIN      CacheHitSource = 3
)

// ── Request/Response types ──

type GetRequest struct {
	Key string `json:"key"`
}

type GetResponse struct {
	Value     []byte         `json:"value"`
	Source    CacheHitSource `json:"source"`
	LatencyNs int64         `json:"latency_ns"`
}

type SetRequest struct {
	Key        string `json:"key"`
	Value      []byte `json:"value"`
	TtlSeconds int64  `json:"ttl_seconds,omitempty"`
}

type SetResponse struct {
	Success bool `json:"success"`
}

type DeleteRequest struct {
	Key string `json:"key"`
}

type DeleteResponse struct {
	Success bool `json:"success"`
}

type BatchGetRequest struct {
	Keys []string `json:"keys"`
}

type CacheEntry struct {
	Value  []byte         `json:"value"`
	Source CacheHitSource `json:"source"`
	Found  bool           `json:"found"`
}

type BatchGetResponse struct {
	Entries map[string]*CacheEntry `json:"entries"`
	Hits    int32                  `json:"hits"`
	Misses  int32                  `json:"misses"`
}

type BatchSetRequest struct {
	Entries    []*KeyValue `json:"entries"`
	TtlSeconds int64      `json:"ttl_seconds,omitempty"`
}

type KeyValue struct {
	Key   string `json:"key"`
	Value []byte `json:"value"`
}

type BatchSetResponse struct {
	SuccessCount int32 `json:"success_count"`
	ErrorCount   int32 `json:"error_count"`
}

type StatsRequest struct{}

type StatsResponse struct {
	L1Hits            int64   `json:"l1_hits"`
	L1Misses          int64   `json:"l1_misses"`
	L1HitRate         float64 `json:"l1_hit_rate"`
	L2Hits            int64   `json:"l2_hits"`
	L2Misses          int64   `json:"l2_misses"`
	L2HitRate         float64 `json:"l2_hit_rate"`
	OriginCalls       int64   `json:"origin_calls"`
	OriginErrors      int64   `json:"origin_errors"`
	CoalescedCalls    int64   `json:"coalesced_calls"`
	TotalRequests     int64   `json:"total_requests"`
	AvgLatencyUs      float64 `json:"avg_latency_us"`
	OverallHitRate    float64 `json:"overall_hit_rate"`
	L1Size            int32   `json:"l1_size"`
	L2PoolActiveConns uint32  `json:"l2_pool_active_conns"`
}

type HealthRequest struct{}

type HealthResponse struct {
	Status         string `json:"status"`
	RedisConnected bool   `json:"redis_connected"`
	UptimeSeconds  int64  `json:"uptime_seconds"`
}
