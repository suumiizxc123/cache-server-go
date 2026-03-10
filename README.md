# Cache Server Go

High-performance multi-layer cache server with binary content delivery (images, video, MPEG-TS).

## Architecture

```
                    ┌─── Production ────────────────────────────────┐
                    │                                                │
  Client ─────────►│  HAProxy (:80)     Load Balancer               │
                    │    │                                           │
                    │    ▼                                           │
                    │  Nginx (:8081)     Disk Cache (2GB) + gzip    │
                    │    ├── /assets/*  → Nginx Cache → MinIO       │
                    │    └── /cache/*   → Go Server                 │
                    │                                                │
                    │  Go Server (:8080) L1 In-Memory + gRPC        │
                    │    ├── L1: Sharded In-Process (50μs)          │
                    │    ├── L2: Redis (0.5ms)                      │
                    │    └── Origin: MinIO / DB (10ms)              │
                    │                                                │
                    │  Redis Master + Replica + Sentinel (HA)       │
                    │  MinIO (S3-compatible object storage)          │
                    │                                                │
                    │  Prometheus → Alertmanager → Slack/Email       │
                    │  Grafana + Loki + Promtail (logs)             │
                    │  Node Exporter + Blackbox (infra)             │
                    └───────────────────────────────────────────────┘
```

## Quick Start

### Dev (fast, minimal)

```bash
make dev              # Nginx + Go + Redis + MinIO
make smoke            # HTTP smoke test
make smoke-assets     # Binary content test
make dev-down         # cleanup
```

### Prod (full stack)

```bash
make prod             # + HAProxy, Redis HA, Prometheus, Grafana, Loki
make prod-ps          # check status
make prod-logs        # all logs
make prod-down        # cleanup
```

### Load Test

```bash
# Generate test assets (28 files, ~100MB, includes MPEG-TS segments)
make generate-assets

# Test against localhost
make loadtest-assets                        # Go server (:8080)
make loadtest-assets-nginx                  # Nginx (:80 dev / :8081 prod)
make loadtest-assets-ha                     # HAProxy → Nginx (:80 prod)

# Test against remote server
make loadtest-assets HOST=172.16.22.24
make loadtest-remote HOST=172.16.22.24      # All 3 modes sequentially

# Heavy load
make loadtest-assets-heavy HOST=172.16.22.24  # 20K reqs, 200 workers
```

### Kubernetes

```bash
make k8s-deploy       # Full stack (namespace + Redis Cluster + app + HPA)
make k8s-status       # Check pods, services, HPA
make k8s-port-forward # localhost:8080 + :9090
make k8s-delete       # Cleanup
```

### Helm

```bash
helm install cache-server deploy/helm/cache-server \
  --namespace cache-system --create-namespace
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

### Binary Assets (port 8080)

| Method | Path | Description |
|--------|------|-------------|
| `POST` | `/upload/{filename}` | Upload to MinIO |
| `GET` | `/assets/{filename}` | Serve from MinIO |
| `GET` | `/meta/{key}` | Asset metadata (Redis) |
| `DELETE` | `/upload/{key}` | Delete asset |
| `GET` | `/list-assets` | List all assets |

### gRPC (port 9090)

| Method | Description |
|--------|-------------|
| `CacheService/Get` | Single key lookup |
| `CacheService/Set` | Write-through |
| `CacheService/Delete` | Invalidate |
| `CacheService/BatchGet` | Multi-key pipeline |
| `CacheService/BatchSet` | Multi-key write |
| `CacheService/Stats` | Metrics |

## Configuration

| Variable | Default | Description |
|----------|---------|-------------|
| `SERVER_PORT` | `8080` | HTTP port |
| `GRPC_PORT` | `9090` | gRPC port |
| `L1_MAX_SIZE` | `100000` | Max L1 entries |
| `L1_TTL` | `30s` | L1 TTL |
| `L2_TTL` | `10m` | L2 TTL |
| `REDIS_ADDRS` | `redis:6379` | Redis address(es) |
| `REDIS_POOL_SIZE` | `500` | Connections per node |
| `MINIO_ENDPOINT` | `minio:9000` | MinIO S3 endpoint |
| `MINIO_BUCKET` | `assets` | MinIO bucket name |
| `ENABLE_SINGLEFLIGHT` | `true` | Request coalescing |

## Project Structure

```
cache-server-go/
├── cmd/server/main.go              # Entry point
├── internal/
│   ├── cache/
│   │   ├── l1.go                   # 256-shard in-process cache
│   │   ├── l2.go                   # Redis client
│   │   └── manager.go             # Two-tier orchestrator
│   ├── binaryhandler/
│   │   ├── handler.go             # Binary content upload/serve (MinIO)
│   │   └── handler_test.go        # Unit tests
│   ├── config/config.go           # Env configuration
│   ├── grpcserver/server.go       # gRPC server
│   ├── handler/handler.go         # HTTP REST handlers
│   ├── metrics/metrics.go         # Atomic counters
│   └── origin/origin.go           # Simulated origin
├── api/
│   ├── proto/cache.proto          # gRPC service definition
│   └── cachepb/cache.go          # Generated types
├── scripts/
│   ├── loadtest.go               # Key-value load test
│   ├── asset_loadtest.go         # Binary asset load test
│   ├── generate_assets.sh        # Generate MPEG-TS + image test files
│   └── grpc_test_client.go       # gRPC test client
├── nginx/nginx.conf              # Reverse proxy + disk cache
├── haproxy/haproxy.cfg           # Load balancer
├── redis/sentinel.conf           # Redis Sentinel
├── prometheus/
│   ├── prometheus.yml            # Scrape config
│   └── alerts.yml               # Alert rules
├── alertmanager/config.yml       # Alert routing
├── blackbox/config.yml           # External health probes
├── promtail/config.yml           # Log shipping to Loki
├── deploy/
│   ├── k8s/                      # Kubernetes manifests
│   ├── helm/cache-server/        # Helm chart
│   └── redis-cluster/            # Redis Cluster compose
├── docker-compose.yml            # DEV stack
├── docker-compose.prod.yml       # PROD overlay
├── .env.prod                     # Prod env vars
├── Dockerfile
└── Makefile
```

## Dev vs Prod

| | Dev (`make dev`) | Prod (`make prod`) |
|---|---|---|
| Nginx | :80 (direct) | :8081 (behind HAProxy) |
| HAProxy | -- | :80, :443, stats :8404 |
| Redis | Single node, 256MB | Master + Replica + Sentinel, 1GB |
| MinIO | :9000, :9001 | :9000, :9001 |
| Monitoring | -- | Prometheus, Alertmanager, Grafana |
| Logging | -- | Loki + Promtail |
| Exporters | -- | Node Exporter, Blackbox |

## Ports

| Port | Service | Mode |
|------|---------|------|
| 80 | Nginx (dev) / HAProxy (prod) | dev / prod |
| 443 | HAProxy (HTTPS) | prod |
| 3000 | Grafana | prod |
| 3100 | Loki | prod |
| 6379 | Redis | both |
| 8080 | Go Cache Server (HTTP) | both |
| 8081 | Nginx (direct) | prod |
| 8404 | HAProxy Stats | prod |
| 9000 | MinIO S3 API | both |
| 9001 | MinIO Console | both |
| 9090 | Go Cache Server (gRPC) | both |
| 9091 | Prometheus | prod |
| 9093 | Alertmanager | prod |
| 9100 | Node Exporter | prod |
| 9115 | Blackbox Exporter | prod |
| 26379 | Redis Sentinel | prod |

## Makefile Targets

```bash
# Build & Run
make build              # Compile Go binary
make run                # Run locally

# Docker
make dev                # Dev stack
make dev-down           # Stop dev
make prod               # Prod stack (full)
make prod-down          # Stop prod
make prod-ps            # Container status
make prod-logs          # Tail all logs

# Test
make test               # Unit tests
make bench              # Benchmarks
make smoke              # HTTP smoke test
make smoke-assets       # Binary content test

# Load Test
make loadtest                            # Key-value load test
make loadtest-assets HOST=<ip>           # Go server
make loadtest-assets-nginx HOST=<ip>     # Nginx direct
make loadtest-assets-ha HOST=<ip>        # HAProxy
make loadtest-remote HOST=<ip>           # All 3 modes
make generate-assets                     # Create test files

# Kubernetes
make k8s-deploy         # Deploy full stack
make k8s-status         # Check status
make k8s-logs           # Tail logs
make k8s-delete         # Cleanup

# Helm
make helm-install       # Install chart
make helm-upgrade       # Upgrade
make helm-uninstall     # Remove
```
