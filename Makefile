# ═══════════════════════════════════════════════════
#  Makefile — Cache Server Build & Run Commands
# ═══════════════════════════════════════════════════

.PHONY: build run test bench dev dev-down prod prod-down clean lint \
        proto redis-cluster redis-cluster-down k8s-deploy k8s-delete helm-install helm-uninstall \
        smoke smoke-grpc smoke-assets generate-assets generate-assets-small \
        loadtest-assets-dev loadtest-assets-dev-heavy loadtest-small-dev loadtest-small-dev-heavy \
        loadtest-small-ha loadtest-small-ha-heavy loadtest-small-ha-ultra \
        loadtest-small-nginx loadtest-small-varnish loadtest-small-ha-100k \
        swarm-init swarm-deploy swarm-down swarm-ps swarm-logs

# ── Build ──
build:
	go build -o bin/cache-server ./cmd/server

# ── Run locally (requires Redis on localhost:6379) ──
run: build
	./bin/cache-server

# ═══════════════════════════════════════
#  Docker — Dev & Prod
# ═══════════════════════════════════════

PROD := --env-file .env.prod -f docker-compose.yml -f docker-compose.prod.yml

# Dev: nginx(:80) + go + redis + minio
dev:
	docker compose up -d --build

dev-down:
	docker compose down -v

dev-logs:
	docker compose logs -f

# Prod: + haproxy, redis HA, monitoring, logging
prod:
	docker compose $(PROD) up -d --build

prod-down:
	docker compose $(PROD) down -v

prod-logs:
	docker compose $(PROD) logs -f

prod-ps:
	docker compose $(PROD) ps

# ── Swarm ──
swarm-init:
	docker swarm init

swarm-deploy:
	docker stack deploy -c docker-compose.yml -c docker-compose.prod.yml cache

swarm-down:
	docker stack rm cache

swarm-ps:
	docker stack services cache

swarm-logs:
	docker service logs -f cache_cache-server

# ═══════════════════════════════════════
#  Redis Cluster (6 nodes)
# ═══════════════════════════════════════

redis-cluster:
	docker compose -f deploy/redis-cluster/docker-compose.redis-cluster.yml up -d

redis-cluster-down:
	docker compose -f deploy/redis-cluster/docker-compose.redis-cluster.yml down -v

# ═══════════════════════════════════════
#  Kubernetes
# ═══════════════════════════════════════

k8s-deploy:
	kubectl apply -f deploy/k8s/namespace.yml
	kubectl apply -f deploy/k8s/configmap.yml
	kubectl apply -f deploy/k8s/redis-cluster.yml
	@echo "Waiting for Redis pods..."
	kubectl -n cache-system wait --for=condition=ready pod -l app=redis-cluster --timeout=120s
	kubectl apply -f deploy/k8s/redis-cluster.yml  # init job
	kubectl apply -f deploy/k8s/service.yml
	kubectl apply -f deploy/k8s/deployment.yml
	kubectl apply -f deploy/k8s/hpa.yml
	@echo ""
	@echo "Cache server deployed! Check status:"
	@echo "  kubectl -n cache-system get pods"
	@echo "  kubectl -n cache-system get svc"

k8s-delete:
	kubectl delete namespace cache-system --ignore-not-found

k8s-status:
	@echo "=== Pods ==="
	kubectl -n cache-system get pods -o wide
	@echo "\n=== Services ==="
	kubectl -n cache-system get svc
	@echo "\n=== HPA ==="
	kubectl -n cache-system get hpa

k8s-logs:
	kubectl -n cache-system logs -l app=cache-server --tail=50 -f

k8s-port-forward:
	@echo "HTTP: http://localhost:8080  |  gRPC: localhost:9090"
	kubectl -n cache-system port-forward svc/cache-server 8080:8080 9090:9090

# ═══════════════════════════════════════
#  Helm
# ═══════════════════════════════════════

helm-install:
	helm install cache-server deploy/helm/cache-server \
		--namespace cache-system --create-namespace

helm-upgrade:
	helm upgrade cache-server deploy/helm/cache-server \
		--namespace cache-system

helm-uninstall:
	helm uninstall cache-server --namespace cache-system

helm-template:
	helm template cache-server deploy/helm/cache-server

# ═══════════════════════════════════════
#  Protobuf (if using protoc)
# ═══════════════════════════════════════

proto:
	protoc --go_out=. --go-grpc_out=. api/proto/cache.proto

# ═══════════════════════════════════════
#  Tests & Benchmarks
# ═══════════════════════════════════════

test:
	go test -v -race -count=1 ./...

bench:
	go test -bench=. -benchmem -count=3 ./internal/cache/...

loadtest:
	go run ./scripts/loadtest.go

loadtest-heavy:
	go run ./scripts/loadtest.go -n 500000 -c 500 -keys 50000

loadtest-duration:
	go run ./scripts/loadtest.go -duration 30s -c 500 -keys 50000

# ── Quick smoke test (HTTP) ──
smoke:
	@echo "=== Health Check ==="
	curl -s http://localhost:8080/health | jq .
	@echo "\n=== Write key ==="
	curl -s -X PUT http://localhost:8080/cache/test-key \
		-H "Content-Type: application/json" \
		-d '{"value":"{\"name\":\"demo\",\"data\":\"hello\"}"}' | jq .
	@echo "\n=== Read key (should hit L1) ==="
	curl -s http://localhost:8080/cache/test-key | jq .
	@echo "\n=== Stats ==="
	curl -s http://localhost:8080/stats | jq .

# ── gRPC smoke test (Go client with JSON codec) ──
smoke-grpc:
	go run ./scripts/grpc_test_client.go

# ═══════════════════════════════════════
#  Binary Assets Smoke Test
# ═══════════════════════════════════════

smoke-assets:
	@echo "══════════════════════════════════════"
	@echo "  Binary Content Cache — Smoke Test"
	@echo "══════════════════════════════════════"
	@echo ""
	@echo "=== 1. Upload SVG via Go server ==="
	curl -s -X POST http://localhost:8080/upload/test.svg \
		--data-binary @sample-assets/test.svg \
		-H "Content-Type: image/svg+xml" | jq .
	@echo ""
	@echo "=== 2. Check metadata in Redis ==="
	curl -s http://localhost:8080/meta/test.svg | jq .
	@echo ""
	@echo "=== 3. List all assets ==="
	curl -s http://localhost:8080/list-assets | jq .
	@echo ""
	@echo "=== 4. Fetch via Nginx (expect MISS) ==="
	curl -sI http://localhost:80/assets/test.svg | grep -E "HTTP|X-Cache|Content-Type|Content-Length"
	@echo ""
	@echo "=== 5. Fetch again (expect HIT) ==="
	curl -sI http://localhost:80/assets/test.svg | grep -E "HTTP|X-Cache|Content-Type|Content-Length"
	@echo ""
	@echo "=== 6. Direct MinIO check ==="
	curl -sI http://localhost:9000/assets/test.svg | grep -E "HTTP|Content-Type|Content-Length"
	@echo ""
	@echo "✅ Binary content caching test complete!"

# ── Generate test assets ──
generate-assets:
	bash scripts/generate_assets.sh

generate-assets-small:
	bash scripts/generate_small_assets.sh 200

# ── Upload all assets + load test binary caching ──
# Use HOST=172.16.22.24 to test against a remote server
# Use --nginx=true to test via Nginx (port 80), default uses Go server (port 8080)
HOST ?= localhost

loadtest-assets:
	go run ./scripts/asset_loadtest.go -host=$(HOST)

loadtest-assets-heavy:
	go run ./scripts/asset_loadtest.go -host=$(HOST) -n 20000 -c 200

# ── Local DEV stack tests (docker-compose.yml) ──
loadtest-assets-dev:
	go run ./scripts/asset_loadtest_local.go

loadtest-assets-dev-heavy:
	go run ./scripts/asset_loadtest_local.go -n 20000 -c 200

loadtest-small-dev:
	go run ./scripts/asset_loadtest_local.go -assets=sample-assets-small

loadtest-small-dev-heavy:
	go run ./scripts/asset_loadtest_local.go -assets=sample-assets-small -n 20000 -c 200

loadtest-assets-nginx:
	go run ./scripts/asset_loadtest.go -host=$(HOST) -nginx=true

loadtest-assets-nginx-heavy:
	go run ./scripts/asset_loadtest.go -host=$(HOST) -n 20000 -c 200 -nginx=true

loadtest-assets-varnish:
	go run ./scripts/asset_loadtest.go -host=$(HOST) -varnish=true

loadtest-assets-varnish-heavy:
	go run ./scripts/asset_loadtest.go -host=$(HOST) -n 20000 -c 200 -varnish=true

loadtest-assets-ha:
	go run ./scripts/asset_loadtest.go -host=$(HOST) -ha=true

loadtest-assets-ha-heavy:
	go run ./scripts/asset_loadtest.go -host=$(HOST) -n 20000 -c 200 -ha=true

# ── Small assets (Varnish RAM optimized, 200 files ~16KB avg) ──
loadtest-small-ha:
	go run ./scripts/asset_loadtest.go -host=$(HOST) -ha=true -assets=sample-assets-small

loadtest-small-ha-heavy:
	go run ./scripts/asset_loadtest.go -host=$(HOST) -ha=true -assets=sample-assets-small -n 20000 -c 200

loadtest-small-ha-ultra:
	go run ./scripts/asset_loadtest.go -host=$(HOST) -ha=true -assets=sample-assets-small -n 50000 -c 500

loadtest-small-ha-100k:
	go run ./scripts/asset_loadtest.go -host=$(HOST) -ha=true -assets=sample-assets-small -n 100000 -c 1000

loadtest-small-nginx:
	go run ./scripts/asset_loadtest.go -host=$(HOST) -nginx=true -assets=sample-assets-small

loadtest-small-varnish:
	go run ./scripts/asset_loadtest.go -host=$(HOST) -varnish=true -assets=sample-assets-small

# ── Remote server tests (all modes) ──
loadtest-remote:
	@echo "══ Go Server (:8080) ══" && go run ./scripts/asset_loadtest.go -host=$(HOST) -read-only
	@echo "══ Nginx Direct (:8081) ══" && go run ./scripts/asset_loadtest.go -host=$(HOST) -nginx=true -read-only
	@echo "══ Varnish Direct (:6081) ══" && go run ./scripts/asset_loadtest.go -host=$(HOST) -varnish=true -read-only
	@echo "══ HAProxy → Varnish → Nginx (:80) ══" && go run ./scripts/asset_loadtest.go -host=$(HOST) -ha=true -read-only

# ── Docker image build ──
docker-build:
	docker build -t cache-server:latest .

# ── Lint ──
lint:
	golangci-lint run ./...

# ── Clean ──
clean:
	rm -rf bin/
	docker compose down -v 2>/dev/null || true
	docker compose -f docker-compose.yml -f docker-compose.prod.yml down -v 2>/dev/null || true
