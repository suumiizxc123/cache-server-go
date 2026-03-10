# ═══════════════════════════════════════════════════
#  Makefile — Cache Server Build & Run Commands
# ═══════════════════════════════════════════════════

.PHONY: build run test bench docker-up docker-down clean lint \
        proto redis-cluster redis-cluster-down k8s-deploy k8s-delete helm-install helm-uninstall \
        smoke smoke-grpc smoke-assets

# ── Build ──
build:
	go build -o bin/cache-server ./cmd/server

# ── Run locally (requires Redis on localhost:6379) ──
run: build
	./bin/cache-server

# ═══════════════════════════════════════
#  Docker
# ═══════════════════════════════════════

# Single Redis + cache server
docker-up:
	docker compose up -d --build

docker-down:
	docker compose down -v

# With Prometheus + Grafana
docker-monitoring:
	docker compose --profile monitoring up -d --build

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

# ── Generate real test assets (various sizes) ──
generate-assets:
	bash scripts/generate_assets.sh

# ── Upload all assets + load test binary caching ──
# Use --nginx=true to test via Nginx (port 80), default uses Go server (port 8080)
loadtest-assets:
	go run ./scripts/asset_loadtest.go

loadtest-assets-heavy:
	go run ./scripts/asset_loadtest.go -n 20000 -c 200

loadtest-assets-nginx:
	go run ./scripts/asset_loadtest.go -nginx=true

loadtest-assets-nginx-heavy:
	go run ./scripts/asset_loadtest.go -n 20000 -c 200 -nginx=true

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
