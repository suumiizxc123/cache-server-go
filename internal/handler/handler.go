package handler

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"

	"github.com/demo/cache-server/internal/cache"
)

// ─────────────────────────────────────────────────────────
// HTTP Handlers
//
// Endpoints:
//   GET  /cache/{key}     — Read from cache (cache-aside)
//   PUT  /cache/{key}     — Write-through to both layers
//   DEL  /cache/{key}     — Invalidate from both layers
//   GET  /stats           — Real-time metrics dashboard
//   GET  /health          — Health check
// ─────────────────────────────────────────────────────────

type Handler struct {
	manager *cache.Manager
	logger  *slog.Logger
}

func New(manager *cache.Manager) *Handler {
	return &Handler{
		manager: manager,
		logger:  slog.Default(),
	}
}

// RegisterRoutes sets up the HTTP mux.
func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /cache/{key}", h.getCache)
	mux.HandleFunc("PUT /cache/{key}", h.putCache)
	mux.HandleFunc("DELETE /cache/{key}", h.deleteCache)
	mux.HandleFunc("GET /stats", h.getStats)
	mux.HandleFunc("GET /health", h.health)

	// Batch endpoint for pipeline testing
	mux.HandleFunc("POST /cache/batch", h.batchGet)
}

// GET /cache/{key}
func (h *Handler) getCache(w http.ResponseWriter, r *http.Request) {
	key := r.PathValue("key")
	if key == "" {
		http.Error(w, `{"error":"key required"}`, http.StatusBadRequest)
		return
	}

	val, err := h.manager.Get(r.Context(), key)
	if err != nil {
		h.logger.Error("cache get failed", "key", key, "error", err)
		http.Error(w, `{"error":"internal error"}`, http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Write(val)
}

// PUT /cache/{key}
func (h *Handler) putCache(w http.ResponseWriter, r *http.Request) {
	key := r.PathValue("key")
	if key == "" {
		http.Error(w, `{"error":"key required"}`, http.StatusBadRequest)
		return
	}

	var body struct {
		Value string `json:"value"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, `{"error":"invalid body"}`, http.StatusBadRequest)
		return
	}

	if err := h.manager.Set(r.Context(), key, []byte(body.Value)); err != nil {
		h.logger.Error("cache set failed", "key", key, "error", err)
		http.Error(w, `{"error":"internal error"}`, http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	w.Write([]byte(`{"status":"ok"}`))
}

// DELETE /cache/{key}
func (h *Handler) deleteCache(w http.ResponseWriter, r *http.Request) {
	key := r.PathValue("key")
	if key == "" {
		http.Error(w, `{"error":"key required"}`, http.StatusBadRequest)
		return
	}

	if err := h.manager.Invalidate(r.Context(), key); err != nil {
		h.logger.Error("cache invalidate failed", "key", key, "error", err)
		http.Error(w, `{"error":"internal error"}`, http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"status":"invalidated"}`))
}

// POST /cache/batch — batch GET for pipelining demo
func (h *Handler) batchGet(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Keys []string `json:"keys"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, `{"error":"invalid body"}`, http.StatusBadRequest)
		return
	}

	results := make(map[string]json.RawMessage, len(body.Keys))
	for _, key := range body.Keys {
		val, err := h.manager.Get(r.Context(), key)
		if err != nil {
			results[key] = json.RawMessage(`{"error":"fetch failed"}`)
			continue
		}
		results[key] = json.RawMessage(val)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(results)
}

// GET /stats — real-time metrics
func (h *Handler) getStats(w http.ResponseWriter, r *http.Request) {
	stats := h.manager.Stats()
	w.Header().Set("Content-Type", "application/json")
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	enc.Encode(stats)
}

// GET /health
func (h *Handler) health(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"status":"healthy","service":"cache-server"}`))
}

// CORS middleware for the dashboard
func CORSMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, PUT, POST, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// LoggingMiddleware logs each request.
func LoggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/health") {
			slog.Info("request", "method", r.Method, "path", r.URL.Path)
		}
		next.ServeHTTP(w, r)
	})
}
