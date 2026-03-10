# Cache Server — Two-Tier Architecture (Go + gRPC + Redis Cluster + K8s)

High-performance distributed cache server designed for **10 Gbps throughput** and **10M requests/second**.

## Architecture

```
                    ┌──────────────────────────────────────────┐
                    │            Kubernetes Cluster             │
                    │                                          │
  Client ──gRPC──► │  Cache Server (HPA: 3–50 pods)           │
  Client ──HTTP──► │    ├── L1: In-Process (50μs, 92% hit)    │
                    │    ├── L2: Redis Cluster (0.5ms)         │
                    │    └── Origin: DB/API (10ms)             │
                    │                                          │
                    │  Redis Cluster (StatefulSet: 6 nodes)    │
                    │    ├── 3 Primary Shards                  │
                    │    └── 3 Replicas (auto-failover)        │
                    └──────────────────────────────────────────┘
```

## Quick Start

### Option 1: Docker (simplest)
```bash
make docker-up          # Redis + cache server
make smoke              # HTTP smoke test
make loadtest           # 100K request benchmark
make docker-down        # cleanup
```

### Option 2: Redis Cluster + Local Server
```bash
make redis-cluster      # 6-node Redis Cluster
REDIS_ADDRS=localhost:6381,localhost:6382,localhost:6383 make run
make smoke-grpc         # gRPC smoke test
```

### Option 3: Kubernetes
```bash
# Build and push image
make docker-build
docker tag cache-server:latest your-registry/cache-server:latest
docker push your-registry/cache-server:latest

# Deploy full stack
make k8s-deploy         # namespace + Redis Cluster + cache server + HPA
make k8s-status         # check pods, services, HPA
make k8s-port-forward   # localhost:8080 (HTTP) + localhost:9090 (gRPC)

# Smoke test
make smoke
make smoke-grpc
```

### Option 4: Helm
```bash
helm install cache-server deploy/helm/cache-server \
  --namespace cache-system --create-namespace \
  --set image.repository=your-registry/cache-server \
  --set redis.addrs="redis-cluster:6379"
```

## API

### HTTP (port 8080)
| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/cache/{key}` | Read (L1 → L2 → Origin) |
| `PUT` | `/cache/{key}` | Write-through |
| `DELETE` | `/cache/{key}` | Invalidate |
| `POST` | `/cache/batch` | Batch GET |
| `GET` | `/stats` | Metrics |
| `GET` | `/health` | Health check |

### gRPC (port 9090)
| Method | Description |
|--------|-------------|
| `CacheService/Get` | Single key lookup |
| `CacheService/Set` | Write-through |
| `CacheService/Delete` | Invalidate |
| `CacheService/BatchGet` | Multi-key pipeline |
| `CacheService/BatchSet` | Multi-key write |
| `CacheService/Stats` | Metrics |
| `CacheService/Health` | Health check |

```bash
# gRPC examples (requires grpcurl)
grpcurl -plaintext -d '{"key":"product:1"}' localhost:9090 cache.v1.CacheService/Get
grpcurl -plaintext localhost:9090 cache.v1.CacheService/Stats
```

## Configuration

| Variable | Default | Description |
|----------|---------|-------------|
| `SERVER_PORT` | `8080` | HTTP port |
| `GRPC_PORT` | `9090` | gRPC port |
| `L1_MAX_SIZE` | `50000` | Max L1 entries |
| `L1_TTL` | `10s` | L1 TTL |
| `REDIS_ADDRS` | `localhost:6379` | Redis Cluster addrs (comma-separated) |
| `REDIS_POOL_SIZE` | `500` | Connections per node |
| `L2_TTL` | `5m` | L2 TTL |
| `ORIGIN_LATENCY` | `10ms` | Simulated DB latency |
| `ENABLE_SINGLEFLIGHT` | `true` | Request coalescing |

## Project Structure

```
cache-server-go/
├── api/
│   ├── proto/cache.proto           # gRPC service definition
│   └── cachepb/cache.go           # Go message types
├── cmd/server/main.go              # Entry point (HTTP + gRPC)
├── internal/
│   ├── cache/
│   │   ├── l1.go                   # 256-shard in-process cache
│   │   ├── l1_test.go             # Tests + benchmarks
│   │   ├── l2.go                   # Redis Cluster client
│   │   └── manager.go             # Two-tier orchestrator
│   ├── config/config.go           # Env configuration
│   ├── grpcserver/server.go       # gRPC server + interceptors
│   ├── handler/handler.go         # HTTP REST handlers
│   ├── metrics/metrics.go         # Atomic counters
│   └── origin/origin.go           # Simulated DB
├── deploy/
│   ├── k8s/
│   │   ├── namespace.yml          # cache-system namespace
│   │   ├── configmap.yml          # Environment config
│   │   ├── deployment.yml         # Cache server (3–50 pods)
│   │   ├── service.yml            # ClusterIP + headless
│   │   ├── hpa.yml                # Auto-scaling + PDB
│   │   ├── redis-cluster.yml      # StatefulSet (6 nodes) + init job
│   │   └── monitoring.yml         # ServiceMonitor + Grafana dashboard
│   ├── redis-cluster/
│   │   └── docker-compose.redis-cluster.yml
│   └── helm/cache-server/
│       ├── Chart.yaml
│       ├── values.yaml
│       └── templates/
├── scripts/
│   ├── loadtest.go                # Zipfian load test
│   └── prometheus.yml
├── docker-compose.yml
├── Dockerfile
├── Makefile
└── go.mod
```

## Kubernetes Scaling

The HPA scales cache-server pods from 3 to 50 based on CPU (70%) and memory (80%):

```
 3 pods  × 21K RPS  =   63K RPS   (idle)
10 pods  × 21K RPS  =  210K RPS   (moderate)
50 pods  × 21K RPS  = 1.05M RPS   (high load)
```

For 10M RPS target: scale to ~500 pods across multiple nodes, upgrade to 20-shard Redis Cluster.
