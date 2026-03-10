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
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	"github.com/redis/go-redis/v9"
)

// ═══════════════════════════════════════════════════════
//  Binary Content Handler
//
//  Handles upload and metadata for binary files:
//    - Images (PNG, JPG, WebP, GIF)
//    - SVG
//    - Video (MP4, WebM)
//    - Any binary content
//
//  Storage: MinIO (S3-compatible) via minio-go SDK
//  Metadata: Redis (fast key lookups)
//  Delivery: Nginx proxy_cache (disk + RAM)
// ═══════════════════════════════════════════════════════

type BinaryHandler struct {
	minioClient *minio.Client
	minioBucket string
	redis       redis.UniversalClient
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

	// Upload to MinIO via SDK (with proper S3 auth)
	// Object key is just the filename — no extra prefix
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

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	fmt.Fprintf(w, `{"status":"uploaded","key":"%s","url":"%s","size":%d,"content_type":"%s"}`,
		objectKey, assetURL, info.Size, contentType)

	slog.Info("asset uploaded", "key", objectKey, "size", info.Size, "type", contentType)
}

// GET /assets/{filename} — Serve binary content from MinIO
func (h *BinaryHandler) serveAsset(w http.ResponseWriter, r *http.Request) {
	filename := r.PathValue("filename")
	if filename == "" {
		http.Error(w, `{"error":"filename required"}`, http.StatusBadRequest)
		return
	}

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

	w.Header().Set("Content-Type", info.ContentType)
	w.Header().Set("Content-Length", fmt.Sprintf("%d", info.Size))
	w.Header().Set("Cache-Control", "public, max-age=86400")
	io.Copy(w, obj)
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

// DELETE /upload/{key} — Delete asset from MinIO + Redis
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
