# ═══════════════════════════════════════════════════
#  Multi-stage build for minimal production image
# ═══════════════════════════════════════════════════

# ── Stage 1: Build ──
FROM golang:1.22-alpine AS builder

RUN apk add --no-cache git ca-certificates

WORKDIR /app

COPY go.mod ./
COPY go.sum* ./

COPY . .

RUN go mod tidy && \
    CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
    go build -ldflags="-w -s" -o /cache-server ./cmd/server

# ── Stage 2: Runtime ──
FROM alpine:3.19

RUN apk add --no-cache ca-certificates tzdata

COPY --from=builder /cache-server /usr/local/bin/cache-server

# Non-root user for security
RUN adduser -D -u 1000 appuser
USER appuser

EXPOSE 8080

ENTRYPOINT ["cache-server"]
