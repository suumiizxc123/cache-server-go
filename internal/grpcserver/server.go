package grpcserver

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"time"

	"github.com/demo/cache-server/api/cachepb"
	"github.com/demo/cache-server/internal/cache"
	"github.com/demo/cache-server/internal/metrics"
	"google.golang.org/grpc"
	"google.golang.org/grpc/encoding"
	"google.golang.org/grpc/keepalive"
)

// ═══════════════════════════════════════════════════════════
//  gRPC Cache Server
//
//  Uses JSON codec (no protoc toolchain required).
//  For production: generate code with protoc for binary encoding.
//
//  Features:
//    - Unary RPCs for Get/Set/Delete/Batch
//    - Keepalive for persistent connections
//    - Logging + metrics interceptors
// ═══════════════════════════════════════════════════════════

// ── JSON Codec (replaces protobuf for demo) ──
// This lets us use plain Go structs without protoc-generated code.

type jsonCodec struct{}

func (jsonCodec) Marshal(v interface{}) ([]byte, error)   { return json.Marshal(v) }
func (jsonCodec) Unmarshal(data []byte, v interface{}) error { return json.Unmarshal(data, v) }
func (jsonCodec) Name() string                             { return "json" }

func init() {
	encoding.RegisterCodec(jsonCodec{})
}

// ── Server ──

type CacheServer struct {
	manager   *cache.Manager
	startTime time.Time
}

func NewCacheServer(manager *cache.Manager) *CacheServer {
	return &CacheServer{
		manager:   manager,
		startTime: time.Now(),
	}
}

// StartGRPC starts the gRPC server on the given port.
func StartGRPC(port int, cacheServer *CacheServer) (*grpc.Server, error) {
	lis, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
	if err != nil {
		return nil, fmt.Errorf("failed to listen on port %d: %w", port, err)
	}

	opts := []grpc.ServerOption{
		grpc.KeepaliveParams(keepalive.ServerParameters{
			MaxConnectionIdle:     5 * time.Minute,
			MaxConnectionAge:      30 * time.Minute,
			MaxConnectionAgeGrace: 10 * time.Second,
			Time:                  1 * time.Minute,
			Timeout:               10 * time.Second,
		}),
		grpc.KeepaliveEnforcementPolicy(keepalive.EnforcementPolicy{
			MinTime:             10 * time.Second,
			PermitWithoutStream: true,
		}),
		grpc.MaxConcurrentStreams(10000),
		grpc.MaxRecvMsgSize(4 * 1024 * 1024),
		grpc.MaxSendMsgSize(4 * 1024 * 1024),
		grpc.ChainUnaryInterceptor(
			loggingInterceptor,
			metricsInterceptor,
		),
	}

	server := grpc.NewServer(opts...)
	registerCacheService(server, cacheServer)

	slog.Info("gRPC server starting", "port", port)

	go func() {
		if err := server.Serve(lis); err != nil {
			slog.Error("gRPC server error", "error", err)
		}
	}()

	return server, nil
}

// ── Service Implementation ──

func (s *CacheServer) Get(ctx context.Context, req *cachepb.GetRequest) (*cachepb.GetResponse, error) {
	if req.Key == "" {
		return nil, fmt.Errorf("key is required")
	}

	start := time.Now()
	val, err := s.manager.Get(ctx, req.Key)
	latency := time.Since(start)

	if err != nil {
		return nil, fmt.Errorf("cache get failed: %v", err)
	}

	return &cachepb.GetResponse{
		Value:     val,
		Source:    cachepb.CacheHitSource_L1,
		LatencyNs: latency.Nanoseconds(),
	}, nil
}

func (s *CacheServer) Set(ctx context.Context, req *cachepb.SetRequest) (*cachepb.SetResponse, error) {
	if req.Key == "" {
		return nil, fmt.Errorf("key is required")
	}
	err := s.manager.Set(ctx, req.Key, req.Value)
	return &cachepb.SetResponse{Success: err == nil}, err
}

func (s *CacheServer) Delete(ctx context.Context, req *cachepb.DeleteRequest) (*cachepb.DeleteResponse, error) {
	if req.Key == "" {
		return nil, fmt.Errorf("key is required")
	}
	err := s.manager.Invalidate(ctx, req.Key)
	return &cachepb.DeleteResponse{Success: err == nil}, err
}

func (s *CacheServer) BatchGet(ctx context.Context, req *cachepb.BatchGetRequest) (*cachepb.BatchGetResponse, error) {
	entries := make(map[string]*cachepb.CacheEntry, len(req.Keys))
	var hits, misses int32
	for _, key := range req.Keys {
		val, err := s.manager.Get(ctx, key)
		if err != nil {
			entries[key] = &cachepb.CacheEntry{Found: false}
			misses++
			continue
		}
		entries[key] = &cachepb.CacheEntry{Value: val, Found: true}
		hits++
	}
	return &cachepb.BatchGetResponse{Entries: entries, Hits: hits, Misses: misses}, nil
}

func (s *CacheServer) BatchSet(ctx context.Context, req *cachepb.BatchSetRequest) (*cachepb.BatchSetResponse, error) {
	var ok, fail int32
	for _, e := range req.Entries {
		if err := s.manager.Set(ctx, e.Key, e.Value); err != nil {
			fail++
		} else {
			ok++
		}
	}
	return &cachepb.BatchSetResponse{SuccessCount: ok, ErrorCount: fail}, nil
}

func (s *CacheServer) Stats(ctx context.Context, req *cachepb.StatsRequest) (*cachepb.StatsResponse, error) {
	st := s.manager.Stats()
	return &cachepb.StatsResponse{
		L1Hits: st.Metrics.L1Hits, L1Misses: st.Metrics.L1Misses, L1HitRate: st.Metrics.L1HitRate,
		L2Hits: st.Metrics.L2Hits, L2Misses: st.Metrics.L2Misses, L2HitRate: st.Metrics.L2HitRate,
		OriginCalls: st.Metrics.OriginCalls, OriginErrors: st.Metrics.OriginErrors,
		CoalescedCalls: st.Metrics.CoalescedCalls, TotalRequests: st.Metrics.TotalRequests,
		AvgLatencyUs: st.Metrics.AvgLatencyUs, OverallHitRate: st.Metrics.OverallHitRate,
		L1Size: int32(st.L1Size), L2PoolActiveConns: st.L2Conns,
	}, nil
}

func (s *CacheServer) Health(ctx context.Context, req *cachepb.HealthRequest) (*cachepb.HealthResponse, error) {
	return &cachepb.HealthResponse{
		Status: "healthy", RedisConnected: true,
		UptimeSeconds: int64(time.Since(s.startTime).Seconds()),
	}, nil
}

// ── Manual Service Registration (JSON codec, no protoc needed) ──

// CacheServiceServer is the interface required by grpc.ServiceDesc.HandlerType
type CacheServiceServer interface {
	Get(context.Context, *cachepb.GetRequest) (*cachepb.GetResponse, error)
	Set(context.Context, *cachepb.SetRequest) (*cachepb.SetResponse, error)
	Delete(context.Context, *cachepb.DeleteRequest) (*cachepb.DeleteResponse, error)
	BatchGet(context.Context, *cachepb.BatchGetRequest) (*cachepb.BatchGetResponse, error)
	BatchSet(context.Context, *cachepb.BatchSetRequest) (*cachepb.BatchSetResponse, error)
	Stats(context.Context, *cachepb.StatsRequest) (*cachepb.StatsResponse, error)
	Health(context.Context, *cachepb.HealthRequest) (*cachepb.HealthResponse, error)
}

func registerCacheService(s *grpc.Server, srv *CacheServer) {
	sd := grpc.ServiceDesc{
		ServiceName: "cache.v1.CacheService",
		HandlerType: (*CacheServiceServer)(nil),
		Methods: []grpc.MethodDesc{
			{MethodName: "Get", Handler: _CacheService_Get_Handler},
			{MethodName: "Set", Handler: _CacheService_Set_Handler},
			{MethodName: "Delete", Handler: _CacheService_Delete_Handler},
			{MethodName: "BatchGet", Handler: _CacheService_BatchGet_Handler},
			{MethodName: "BatchSet", Handler: _CacheService_BatchSet_Handler},
			{MethodName: "Stats", Handler: _CacheService_Stats_Handler},
			{MethodName: "Health", Handler: _CacheService_Health_Handler},
		},
		Streams:  []grpc.StreamDesc{},
		Metadata: "cache.proto",
	}
	s.RegisterService(&sd, srv)
}

// Individual typed handlers (required by grpc.MethodDesc)

func _CacheService_Get_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(cachepb.GetRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(*CacheServer).Get(ctx, in)
	}
	info := &grpc.UnaryServerInfo{Server: srv, FullMethod: "/cache.v1.CacheService/Get"}
	return interceptor(ctx, in, info, func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(*CacheServer).Get(ctx, req.(*cachepb.GetRequest))
	})
}

func _CacheService_Set_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(cachepb.SetRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(*CacheServer).Set(ctx, in)
	}
	info := &grpc.UnaryServerInfo{Server: srv, FullMethod: "/cache.v1.CacheService/Set"}
	return interceptor(ctx, in, info, func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(*CacheServer).Set(ctx, req.(*cachepb.SetRequest))
	})
}

func _CacheService_Delete_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(cachepb.DeleteRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(*CacheServer).Delete(ctx, in)
	}
	info := &grpc.UnaryServerInfo{Server: srv, FullMethod: "/cache.v1.CacheService/Delete"}
	return interceptor(ctx, in, info, func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(*CacheServer).Delete(ctx, req.(*cachepb.DeleteRequest))
	})
}

func _CacheService_BatchGet_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(cachepb.BatchGetRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(*CacheServer).BatchGet(ctx, in)
	}
	info := &grpc.UnaryServerInfo{Server: srv, FullMethod: "/cache.v1.CacheService/BatchGet"}
	return interceptor(ctx, in, info, func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(*CacheServer).BatchGet(ctx, req.(*cachepb.BatchGetRequest))
	})
}

func _CacheService_BatchSet_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(cachepb.BatchSetRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(*CacheServer).BatchSet(ctx, in)
	}
	info := &grpc.UnaryServerInfo{Server: srv, FullMethod: "/cache.v1.CacheService/BatchSet"}
	return interceptor(ctx, in, info, func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(*CacheServer).BatchSet(ctx, req.(*cachepb.BatchSetRequest))
	})
}

func _CacheService_Stats_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(cachepb.StatsRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(*CacheServer).Stats(ctx, in)
	}
	info := &grpc.UnaryServerInfo{Server: srv, FullMethod: "/cache.v1.CacheService/Stats"}
	return interceptor(ctx, in, info, func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(*CacheServer).Stats(ctx, req.(*cachepb.StatsRequest))
	})
}

func _CacheService_Health_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(cachepb.HealthRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(*CacheServer).Health(ctx, in)
	}
	info := &grpc.UnaryServerInfo{Server: srv, FullMethod: "/cache.v1.CacheService/Health"}
	return interceptor(ctx, in, info, func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(*CacheServer).Health(ctx, req.(*cachepb.HealthRequest))
	})
}

// ── Interceptors ──

func loggingInterceptor(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
	start := time.Now()
	resp, err := handler(ctx, req)
	d := time.Since(start)
	if err != nil {
		slog.Error("gRPC error", "method", info.FullMethod, "duration", d, "error", err)
	} else if d > 50*time.Millisecond {
		slog.Warn("gRPC slow", "method", info.FullMethod, "duration", d)
	}
	return resp, err
}

func metricsInterceptor(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
	start := time.Now()
	resp, err := handler(ctx, req)
	metrics.Global.RecordLatency(time.Since(start))
	return resp, err
}
