package main

import (
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/demo/cache-server/internal/binaryhandler"
	"github.com/demo/cache-server/internal/cache"
	"github.com/demo/cache-server/internal/config"
	"github.com/demo/cache-server/internal/grpcserver"
	"github.com/demo/cache-server/internal/handler"
	"github.com/demo/cache-server/internal/origin"
)

// ═══════════════════════════════════════════════════════════
//  Cache Server — Two-Tier Architecture
//
//  Protocols:
//    - HTTP/1.1 (REST API on :8080)
//    - gRPC     (binary protocol on :9090)
//
//  Architecture:
//    Client → [HTTP|gRPC] → L1 (In-Process) → L2 (Redis Cluster) → Origin
//
//  Target:  10 Gbps throughput, 10M requests/second
//  Design:  Cache-aside + singleflight + two-tier eviction
// ═══════════════════════════════════════════════════════════

func main() {
	// Structured logging
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))
	slog.SetDefault(logger)

	cfg := config.Load()

	slog.Info("starting cache server",
		"http_addr", fmt.Sprintf("%s:%d", cfg.ServerAddr, cfg.ServerPort),
		"grpc_port", cfg.GRPCPort,
		"l1_max_size", cfg.L1MaxSize,
		"l1_ttl", cfg.L1TTL,
		"redis_addrs", cfg.RedisAddrs,
		"redis_pool_size", cfg.RedisPoolSize,
		"l2_ttl", cfg.L2TTL,
		"singleflight", cfg.EnableSingleflight,
	)

	// ── Initialize L1 (In-Process Cache) ──
	l1 := cache.NewL1(cfg.L1MaxSize, cfg.L1TTL)
	slog.Info("L1 cache initialized", "max_size", cfg.L1MaxSize, "ttl", cfg.L1TTL)

	// ── Initialize L2 (Redis Cluster) ──
	l2, err := cache.NewL2(cfg.RedisAddrs, cfg.RedisPassword, cfg.RedisPoolSize, cfg.L2TTL)
	if err != nil {
		slog.Error("failed to connect to Redis", "error", err)
		slog.Info("running in L1-only mode (degraded)")
		l2 = mustCreateFallbackL2(cfg)
	}
	defer l2.Close()
	slog.Info("L2 Redis connected", "addrs", cfg.RedisAddrs, "pool_size", cfg.RedisPoolSize)

	// ── Initialize Origin (simulated DB) ──
	sim := origin.NewSimulated(cfg.OriginLatency)
	slog.Info("origin configured", "base_latency", cfg.OriginLatency)

	// ── Create Cache Manager ──
	mgr := cache.NewManager(l1, l2, sim.Fetch, cfg.EnableSingleflight)

	// ═══════════════════════════════════════
	//  Start gRPC Server
	// ═══════════════════════════════════════
	grpcSvc := grpcserver.NewCacheServer(mgr)
	grpcSrv, err := grpcserver.StartGRPC(cfg.GRPCPort, grpcSvc)
	if err != nil {
		slog.Error("failed to start gRPC server", "error", err)
		os.Exit(1)
	}
	slog.Info("gRPC server started", "port", cfg.GRPCPort)

	// ═══════════════════════════════════════
	//  Start HTTP Server (REST + metrics)
	// ═══════════════════════════════════════
	mux := http.NewServeMux()
	h := handler.New(mgr)
	h.RegisterRoutes(mux)

	// ── Binary Content Handler (images, SVG, video) ──
	binHandler := binaryhandler.New(
		cfg.MinioEndpoint, cfg.MinioBucket,
		cfg.MinioAccessKey, cfg.MinioSecretKey,
		l2.Client(),
	)
	binHandler.RegisterRoutes(mux)

	var httpHandler http.Handler = mux
	httpHandler = handler.CORSMiddleware(httpHandler)
	httpHandler = handler.LoggingMiddleware(httpHandler)

	server := &http.Server{
		Addr:           fmt.Sprintf("%s:%d", cfg.ServerAddr, cfg.ServerPort),
		Handler:        httpHandler,
		ReadTimeout:    5 * time.Second,
		WriteTimeout:   10 * time.Second,
		IdleTimeout:    120 * time.Second,
		MaxHeaderBytes: 1 << 16,
	}

	// ── Graceful Shutdown ──
	go func() {
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
		sig := <-sigCh
		slog.Info("shutting down", "signal", sig)

		// Graceful gRPC shutdown (drain connections)
		grpcSrv.GracefulStop()
		slog.Info("gRPC server stopped")

		// Graceful HTTP shutdown
		server.Close()
	}()

	slog.Info("cache server ready",
		"http", fmt.Sprintf("http://%s:%d", cfg.ServerAddr, cfg.ServerPort),
		"grpc", fmt.Sprintf(":%d", cfg.GRPCPort),
		"http_routes", []string{
			"GET    /cache/{key}",
			"PUT    /cache/{key}",
			"DELETE /cache/{key}",
			"POST   /cache/batch",
			"GET    /stats",
			"GET    /health",
		},
		"grpc_methods", []string{
			"cache.v1.CacheService/Get",
			"cache.v1.CacheService/Set",
			"cache.v1.CacheService/Delete",
			"cache.v1.CacheService/BatchGet",
			"cache.v1.CacheService/BatchSet",
			"cache.v1.CacheService/Stats",
			"cache.v1.CacheService/Health",
		},
	)

	if err := server.ListenAndServe(); err != http.ErrServerClosed {
		slog.Error("server error", "error", err)
		os.Exit(1)
	}

	slog.Info("server stopped")
}

func mustCreateFallbackL2(cfg *config.Config) *cache.L2Cache {
	l2, err := cache.NewL2(cfg.RedisAddrs, cfg.RedisPassword, cfg.RedisPoolSize, cfg.L2TTL)
	if err != nil {
		slog.Warn("L2 still unavailable — running in degraded mode")
		l2, _ = cache.NewL2([]string{"localhost:6379"}, "", 1, cfg.L2TTL)
		return l2
	}
	return l2
}
