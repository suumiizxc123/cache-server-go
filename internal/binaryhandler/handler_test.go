package binaryhandler

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/minio/minio-go/v7"
	"github.com/redis/go-redis/v9"
)

// ═══════════════════════════════════════════════════════
//  BinaryHandler — Unit Tests
//
//  Requires running MinIO + Redis (docker compose up).
//  Tests: upload, serve, metadata, delete, list
// ═══════════════════════════════════════════════════════

const (
	testMinioEndpoint = "localhost:9000"
	testMinioBucket   = "assets"
	testAccessKey     = "minioadmin"
	testSecretKey     = "minioadmin"
	testRedisAddr     = "localhost:6379"
)

func setupHandler(t *testing.T) (*BinaryHandler, *http.ServeMux) {
	t.Helper()

	rc := redis.NewClient(&redis.Options{Addr: testRedisAddr})
	if err := rc.Ping(context.Background()).Err(); err != nil {
		t.Skipf("Redis not available: %v", err)
	}

	h := New(testMinioEndpoint, testMinioBucket, testAccessKey, testSecretKey, rc)

	// Verify MinIO connectivity
	_, err := h.minioClient.BucketExists(context.Background(), testMinioBucket)
	if err != nil {
		t.Skipf("MinIO not available: %v", err)
	}

	mux := http.NewServeMux()
	h.RegisterRoutes(mux)
	return h, mux
}

func cleanup(t *testing.T, h *BinaryHandler, key string) {
	t.Helper()
	ctx := context.Background()
	h.minioClient.RemoveObject(ctx, h.minioBucket, key, minio.RemoveObjectOptions{})
	h.redis.Del(ctx, "asset:"+key)
	h.redis.SRem(ctx, "assets:index", key)
}

func TestUploadAndServe(t *testing.T) {
	h, mux := setupHandler(t)
	key := "test-upload-serve.txt"
	defer cleanup(t, h, key)

	payload := []byte("hello binary content")

	// Upload
	req := httptest.NewRequest("POST", "/upload/"+key, bytes.NewReader(payload))
	req.Header.Set("Content-Type", "text/plain")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("upload: expected 201, got %d: %s", w.Code, w.Body.String())
	}

	// Serve
	req = httptest.NewRequest("GET", "/assets/"+key, nil)
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("serve: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if !bytes.Equal(w.Body.Bytes(), payload) {
		t.Fatalf("serve: expected %q, got %q", payload, w.Body.Bytes())
	}
	if ct := w.Header().Get("Content-Type"); ct != "text/plain" {
		t.Fatalf("serve: expected Content-Type text/plain, got %s", ct)
	}
}

func TestServeNotFound(t *testing.T) {
	_, mux := setupHandler(t)

	req := httptest.NewRequest("GET", "/assets/nonexistent-file-xyz.bin", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

func TestGetMeta(t *testing.T) {
	h, mux := setupHandler(t)
	key := "test-meta.svg"
	defer cleanup(t, h, key)

	// Upload first
	payload := []byte("<svg></svg>")
	req := httptest.NewRequest("POST", "/upload/"+key, bytes.NewReader(payload))
	req.Header.Set("Content-Type", "image/svg+xml")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("upload: expected 201, got %d", w.Code)
	}

	// Get metadata
	req = httptest.NewRequest("GET", "/meta/"+key, nil)
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("meta: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	if !bytes.Contains([]byte(body), []byte(`"content_type":"image/svg+xml"`)) {
		t.Fatalf("meta: expected content_type in response, got %s", body)
	}
}

func TestDeleteAsset(t *testing.T) {
	h, mux := setupHandler(t)
	key := "test-delete.png"
	defer cleanup(t, h, key)

	// Upload
	req := httptest.NewRequest("POST", "/upload/"+key, bytes.NewReader([]byte("fake png")))
	req.Header.Set("Content-Type", "image/png")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("upload: expected 201, got %d", w.Code)
	}

	// Delete
	req = httptest.NewRequest("DELETE", "/upload/"+key, nil)
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("delete: expected 200, got %d", w.Code)
	}

	// Verify gone from MinIO
	req = httptest.NewRequest("GET", "/assets/"+key, nil)
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404 after delete, got %d", w.Code)
	}

	// Verify gone from Redis
	req = httptest.NewRequest("GET", "/meta/"+key, nil)
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for meta after delete, got %d", w.Code)
	}
}

func TestListAssets(t *testing.T) {
	h, mux := setupHandler(t)
	key := "test-list.jpg"
	defer cleanup(t, h, key)

	// Upload
	req := httptest.NewRequest("POST", "/upload/"+key, bytes.NewReader([]byte("fake jpg")))
	req.Header.Set("Content-Type", "image/jpeg")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("upload: expected 201, got %d", w.Code)
	}

	// List
	req = httptest.NewRequest("GET", "/list-assets", nil)
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("list: expected 200, got %d", w.Code)
	}
	if !bytes.Contains(w.Body.Bytes(), []byte(key)) {
		t.Fatalf("list: expected %s in response, got %s", key, w.Body.String())
	}
}

func TestUploadLargeFile(t *testing.T) {
	h, mux := setupHandler(t)
	key := "test-large.bin"
	defer cleanup(t, h, key)

	// 1MB payload
	payload := make([]byte, 1<<20)
	for i := range payload {
		payload[i] = byte(i % 256)
	}

	req := httptest.NewRequest("POST", "/upload/"+key, bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/octet-stream")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("upload: expected 201, got %d: %s", w.Code, w.Body.String())
	}

	// Serve and verify content matches
	req = httptest.NewRequest("GET", "/assets/"+key, nil)
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("serve: expected 200, got %d", w.Code)
	}
	if w.Body.Len() != len(payload) {
		t.Fatalf("serve: expected %d bytes, got %d", len(payload), w.Body.Len())
	}
}

func TestDetectContentType(t *testing.T) {
	tests := []struct {
		filename string
		expected string
	}{
		{"photo.png", "image/png"},
		{"photo.jpg", "image/jpeg"},
		{"photo.jpeg", "image/jpeg"},
		{"icon.svg", "image/svg+xml"},
		{"clip.mp4", "video/mp4"},
		{"font.woff2", "font/woff2"},
		{"style.css", "text/css"},
		{"app.js", "application/javascript"},
		{"unknown.xyz", "application/octet-stream"},
	}

	for _, tt := range tests {
		got := detectContentType(tt.filename)
		if got != tt.expected {
			t.Errorf("detectContentType(%q) = %q, want %q", tt.filename, got, tt.expected)
		}
	}
}

func TestUploadContentTypeDetection(t *testing.T) {
	h, mux := setupHandler(t)
	key := "test-autodetect.png"
	defer cleanup(t, h, key)

	// Upload without Content-Type header — should auto-detect from extension
	req := httptest.NewRequest("POST", "/upload/"+key, bytes.NewReader([]byte("fake png")))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("upload: expected 201, got %d: %s", w.Code, w.Body.String())
	}

	// Serve and check content type
	req = httptest.NewRequest("GET", "/assets/"+key, nil)
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if ct := w.Header().Get("Content-Type"); ct != "image/png" {
		t.Fatalf("expected auto-detected Content-Type image/png, got %s", ct)
	}
}

// ── Benchmarks ──

func BenchmarkServeAsset(b *testing.B) {
	rc := redis.NewClient(&redis.Options{Addr: testRedisAddr})
	if err := rc.Ping(context.Background()).Err(); err != nil {
		b.Skipf("Redis not available: %v", err)
	}

	h := New(testMinioEndpoint, testMinioBucket, testAccessKey, testSecretKey, rc)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	// Upload a test file
	key := "bench-asset.txt"
	payload := bytes.Repeat([]byte("benchmark data "), 100)
	req := httptest.NewRequest("POST", "/upload/"+key, bytes.NewReader(payload))
	req.Header.Set("Content-Type", "text/plain")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		b.Fatalf("upload failed: %d", w.Code)
	}
	defer func() {
		ctx := context.Background()
		h.minioClient.RemoveObject(ctx, h.minioBucket, key, minio.RemoveObjectOptions{})
		h.redis.Del(ctx, "asset:"+key)
		h.redis.SRem(ctx, "assets:index", key)
	}()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		req := httptest.NewRequest("GET", "/assets/"+key, nil)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)
		io.Copy(io.Discard, w.Body)
	}
}
