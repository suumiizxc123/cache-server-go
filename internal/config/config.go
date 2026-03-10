package config

import (
	"os"
	"strconv"
	"time"
)

type Config struct {
	// Server
	ServerAddr string
	ServerPort int
	GRPCPort   int

	// L1 Cache (In-Process)
	L1MaxSize int
	L1TTL     time.Duration

	// L2 Cache (Redis Cluster)
	RedisAddrs    []string
	RedisPassword string
	RedisPoolSize int
	L2TTL         time.Duration

	// Origin
	OriginLatency time.Duration // simulated origin latency

	// MinIO (S3-compatible object storage)
	MinioEndpoint  string
	MinioBucket    string
	MinioAccessKey string
	MinioSecretKey string

	// Singleflight
	EnableSingleflight bool
}

func Load() *Config {
	return &Config{
		ServerAddr:         envStr("SERVER_ADDR", "0.0.0.0"),
		ServerPort:         envInt("SERVER_PORT", 8080),
		GRPCPort:           envInt("GRPC_PORT", 9090),
		L1MaxSize:          envInt("L1_MAX_SIZE", 50000),
		L1TTL:              envDuration("L1_TTL", 10*time.Second),
		RedisAddrs:         envSlice("REDIS_ADDRS", []string{"localhost:6379"}),
		RedisPassword:      envStr("REDIS_PASSWORD", ""),
		RedisPoolSize:      envInt("REDIS_POOL_SIZE", 500),
		L2TTL:              envDuration("L2_TTL", 5*time.Minute),
		OriginLatency:      envDuration("ORIGIN_LATENCY", 10*time.Millisecond),
		MinioEndpoint:      envStr("MINIO_ENDPOINT", "minio:9000"),
		MinioBucket:        envStr("MINIO_BUCKET", "assets"),
		MinioAccessKey:     envStr("MINIO_ACCESS_KEY", "minioadmin"),
		MinioSecretKey:     envStr("MINIO_SECRET_KEY", "minioadmin"),
		EnableSingleflight: envBool("ENABLE_SINGLEFLIGHT", true),
	}
}

func envStr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func envInt(key string, fallback int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return fallback
}

func envBool(key string, fallback bool) bool {
	if v := os.Getenv(key); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			return b
		}
	}
	return fallback
}

func envDuration(key string, fallback time.Duration) time.Duration {
	if v := os.Getenv(key); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return fallback
}

func envSlice(key string, fallback []string) []string {
	if v := os.Getenv(key); v != "" {
		// simple comma split
		result := []string{}
		current := ""
		for _, c := range v {
			if c == ',' {
				if current != "" {
					result = append(result, current)
				}
				current = ""
			} else {
				current += string(c)
			}
		}
		if current != "" {
			result = append(result, current)
		}
		if len(result) > 0 {
			return result
		}
	}
	return fallback
}
