package binaryhandler

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	"github.com/redis/go-redis/v9"
)

// ═══════════════════════════════════════════════════════
//  Binary Content Handler
//
//  L1: In-memory LRU cache (up to 512MB)
//  L2: MinIO (S3-compatible object storage)
//  Metadata: Redis
// ═══════════════════════════════════════════════════════

// cachedAsset holds binary content + metadata in memory.
type cachedAsset struct {
	data        []byte
	contentType string
	size        int64
	cachedAt    time.Time
}

// assetCache is a simple concurrent cache with size-based eviction.
type assetCache struct {
	mu       sync.RWMutex
	items    map[string]*cachedAsset
	maxBytes int64
	curBytes int64
	hits     atomic.Int64
	misses   atomic.Int64
}

func newAssetCache(maxBytes int64) *assetCache {
	return &assetCache{
		items:    make(map[string]*cachedAsset),
		maxBytes: maxBytes,
	}
}

func (c *assetCache) get(key string) (*cachedAsset, bool) {
	c.mu.RLock()
	item, ok := c.items[key]
	c.mu.RUnlock()
	if ok {
		c.hits.Add(1)
		return item, true
	}
	c.misses.Add(1)
	return nil, false
}

func (c *assetCache) set(key string, asset *cachedAsset) {
	c.mu.Lock()
	defer c.mu.Unlock()

	// If already exists, subtract old size
	if old, ok := c.items[key]; ok {
		c.curBytes -= int64(len(old.data))
	}

	// Evict oldest entries if over budget
	newSize := int64(len(asset.data))
	for c.curBytes+newSize > c.maxBytes && len(c.items) > 0 {
		var oldestKey string
		var oldestTime time.Time
		for k, v := range c.items {
			if oldestKey == "" || v.cachedAt.Before(oldestTime) {
				oldestKey = k
				oldestTime = v.cachedAt
			}
		}
		if oldestKey != "" {
			c.curBytes -= int64(len(c.items[oldestKey].data))
			delete(c.items, oldestKey)
		}
	}

	c.items[key] = asset
	c.curBytes += newSize
}

func (c *assetCache) remove(key string) {
	c.mu.Lock()
	if item, ok := c.items[key]; ok {
		c.curBytes -= int64(len(item.data))
		delete(c.items, key)
	}
	c.mu.Unlock()
}

type BinaryHandler struct {
	minioClient *minio.Client
	minioBucket string
	redis       redis.UniversalClient
	cache       *assetCache
}

type AssetMeta struct {
	Key         string `json:"key"`
	ContentType string `json:"content_type"`
	Size        int64  `json:"size"`
	URL         string `json:"url"`
	UploadedAt  int64  `json:"uploaded_at"`
}

func New(minioEndpoint, minioBucket, accessKey, secretKey string, redisClient redis.UniversalClient) *BinaryHandler {
	client, err := minio.New(minioEndpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(accessKey, secretKey, ""),
		Secure: false,
	})
	if err != nil {
		slog.Error("failed to create MinIO client", "error", err)
	}
	return &BinaryHandler{
		minioClient: client,
		minioBucket: minioBucket,
		redis:       redisClient,
		cache:       newAssetCache(512 * 1024 * 1024), // 512MB in-memory cache
	}
}

// RegisterRoutes adds binary content routes to the mux.
func (h *BinaryHandler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /upload/{filename...}", h.upload)
	mux.HandleFunc("GET /assets/{filename...}", h.serveAsset)
	mux.HandleFunc("GET /meta/{key...}", h.getMeta)
	mux.HandleFunc("DELETE /upload/{key...}", h.deleteAsset)
	mux.HandleFunc("GET /list-assets", h.listAssets)
}

// POST /upload/{filename} — Upload binary content to MinIO
func (h *BinaryHandler) upload(w http.ResponseWriter, r *http.Request) {
	filename := r.PathValue("filename")
	if filename == "" {
		http.Error(w, `{"error":"filename required"}`, http.StatusBadRequest)
		return
	}

	// Detect content type
	contentType := r.Header.Get("Content-Type")
	if contentType == "" || contentType == "application/octet-stream" {
		contentType = detectContentType(filename)
	}

	// Read body
	body, err := io.ReadAll(io.LimitReader(r.Body, 100*1024*1024)) // 100MB max
	if err != nil {
		http.Error(w, `{"error":"failed to read body"}`, http.StatusBadRequest)
		return
	}
	defer r.Body.Close()

	objectKey := filename
	reader := bytes.NewReader(body)

	info, err := h.minioClient.PutObject(r.Context(), h.minioBucket, objectKey, reader, int64(len(body)),
		minio.PutObjectOptions{
			ContentType: contentType,
		},
	)
	if err != nil {
		slog.Error("MinIO upload failed", "error", err, "key", objectKey)
		http.Error(w, fmt.Sprintf(`{"error":"storage upload failed: %s"}`, err.Error()), http.StatusBadGateway)
		return
	}

	// Store metadata in Redis
	ctx := r.Context()
	assetURL := fmt.Sprintf("/assets/%s", filename)

	h.redis.HSet(ctx, "asset:"+objectKey, map[string]interface{}{
		"content_type": contentType,
		"size":         info.Size,
		"url":          assetURL,
		"uploaded_at":  time.Now().Unix(),
	})
	h.redis.SAdd(ctx, "assets:index", objectKey)

	// Pre-populate in-memory cache
	h.cache.set(objectKey, &cachedAsset{
		data:        body,
		contentType: contentType,
		size:        int64(len(body)),
		cachedAt:    time.Now(),
	})

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	fmt.Fprintf(w, `{"status":"uploaded","key":"%s","url":"%s","size":%d,"content_type":"%s"}`,
		objectKey, assetURL, info.Size, contentType)

	slog.Info("asset uploaded", "key", objectKey, "size", info.Size, "type", contentType)
}

// GET /assets/{filename} — Serve binary content (L1 memory → L2 MinIO)
func (h *BinaryHandler) serveAsset(w http.ResponseWriter, r *http.Request) {
	filename := r.PathValue("filename")
	if filename == "" {
		http.Error(w, `{"error":"filename required"}`, http.StatusBadRequest)
		return
	}

	// L1: Check in-memory cache first
	if cached, ok := h.cache.get(filename); ok {
		w.Header().Set("Content-Type", cached.contentType)
		w.Header().Set("Content-Length", fmt.Sprintf("%d", cached.size))
		w.Header().Set("Cache-Control", "public, max-age=86400")
		w.Header().Set("X-Cache-Status", "HIT")
		w.Write(cached.data)
		return
	}

	// L2: Fetch from MinIO
	obj, err := h.minioClient.GetObject(r.Context(), h.minioBucket, filename, minio.GetObjectOptions{})
	if err != nil {
		http.Error(w, `{"error":"not found"}`, http.StatusNotFound)
		return
	}
	defer obj.Close()

	info, err := obj.Stat()
	if err != nil {
		http.Error(w, `{"error":"not found"}`, http.StatusNotFound)
		return
	}

	// Read full content to cache it
	data, err := io.ReadAll(obj)
	if err != nil {
		http.Error(w, `{"error":"read failed"}`, http.StatusInternalServerError)
		return
	}

	// Cache for next time
	h.cache.set(filename, &cachedAsset{
		data:        data,
		contentType: info.ContentType,
		size:        info.Size,
		cachedAt:    time.Now(),
	})

	w.Header().Set("Content-Type", info.ContentType)
	w.Header().Set("Content-Length", fmt.Sprintf("%d", info.Size))
	w.Header().Set("Cache-Control", "public, max-age=86400")
	w.Header().Set("X-Cache-Status", "MISS")
	w.Write(data)
}

// GET /meta/{key} — Get asset metadata from Redis
func (h *BinaryHandler) getMeta(w http.ResponseWriter, r *http.Request) {
	key := r.PathValue("key")
	if key == "" {
		http.Error(w, `{"error":"key required"}`, http.StatusBadRequest)
		return
	}

	result, err := h.redis.HGetAll(r.Context(), "asset:"+key).Result()
	if err != nil || len(result) == 0 {
		http.Error(w, `{"error":"not found"}`, http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	fmt.Fprintf(w, `{"key":"%s","content_type":"%s","size":%s,"url":"%s","uploaded_at":%s}`,
		key, result["content_type"], result["size"], result["url"], result["uploaded_at"])
}

// DELETE /upload/{key} — Delete asset from MinIO + Redis + cache
func (h *BinaryHandler) deleteAsset(w http.ResponseWriter, r *http.Request) {
	key := r.PathValue("key")
	if key == "" {
		http.Error(w, `{"error":"key required"}`, http.StatusBadRequest)
		return
	}

	ctx := r.Context()

	// Delete from MinIO
	err := h.minioClient.RemoveObject(ctx, h.minioBucket, key, minio.RemoveObjectOptions{})
	if err != nil {
		slog.Warn("MinIO delete failed", "error", err, "key", key)
	}

	// Delete metadata from Redis
	h.redis.Del(ctx, "asset:"+key)
	h.redis.SRem(ctx, "assets:index", key)

	// Delete from in-memory cache
	h.cache.remove(key)

	w.Header().Set("Content-Type", "application/json")
	fmt.Fprintf(w, `{"status":"deleted","key":"%s"}`, key)
}

// GET /list-assets — List all asset keys
func (h *BinaryHandler) listAssets(w http.ResponseWriter, r *http.Request) {
	keys, err := h.redis.SMembers(context.Background(), "assets:index").Result()
	if err != nil {
		http.Error(w, `{"error":"failed to list"}`, http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	fmt.Fprintf(w, `{"count":%d,"assets":[`, len(keys))
	for i, key := range keys {
		if i > 0 {
			fmt.Fprint(w, ",")
		}
		fmt.Fprintf(w, `"%s"`, key)
	}
	fmt.Fprint(w, "]}")
}

// detectContentType returns MIME type based on file extension.
func detectContentType(filename string) string {
	ext := strings.ToLower(filepath.Ext(filename))
	switch ext {
	case ".png":
		return "image/png"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".gif":
		return "image/gif"
	case ".webp":
		return "image/webp"
	case ".svg":
		return "image/svg+xml"
	case ".mp4":
		return "video/mp4"
	case ".ts":
		return "video/mp2t"
	case ".m3u8":
		return "application/vnd.apple.mpegurl"
	case ".webm":
		return "video/webm"
	case ".mov":
		return "video/quicktime"
	case ".avif":
		return "image/avif"
	case ".ico":
		return "image/x-icon"
	case ".woff2":
		return "font/woff2"
	case ".woff":
		return "font/woff"
	case ".ttf":
		return "font/ttf"
	case ".css":
		return "text/css"
	case ".js":
		return "application/javascript"
	default:
		return "application/octet-stream"
	}
}
