package httpapi

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"centerseat/backend/internal/domain"
	"centerseat/backend/internal/service"
)

type cachedResult struct {
	bodyHash string
	body     []byte
	etag     string
	expires  time.Time
}

type Server struct {
	service    *service.Service
	logger     *slog.Logger
	mux        *http.ServeMux
	mu         sync.RWMutex
	results    map[string]cachedResult
	byKey      map[string]string
	requests   atomic.Uint64
	errors     atomic.Uint64
	durationMS atomic.Uint64
}

func New(svc *service.Service, logger *slog.Logger) *Server {
	if logger == nil {
		logger = slog.Default()
	}
	s := &Server{service: svc, logger: logger, mux: http.NewServeMux(), results: map[string]cachedResult{}, byKey: map[string]string{}}
	s.routes()
	return s
}

func (s *Server) routes() {
	s.mux.HandleFunc("GET /healthz", s.health)
	s.mux.HandleFunc("GET /readyz", s.ready)
	s.mux.HandleFunc("GET /metrics", s.metrics)
	s.mux.HandleFunc("GET /v1/providers", s.providers)
	s.mux.HandleFunc("POST /v1/showtime-queries", s.createShowtimeQuery)
	s.mux.HandleFunc("POST /v1/seat-queries", s.createQuery)
	s.mux.HandleFunc("GET /v1/seat-queries/{query_id}", s.getQuery)
}

func (s *Server) ready(w http.ResponseWriter, _ *http.Request) {
	for _, provider := range s.service.ProviderStatuses() {
		if !provider.Configured || provider.Status != "healthy" {
			writeJSON(w, http.StatusServiceUnavailable, map[string]any{"status": "not_ready", "provider": provider.Name})
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "ready", "time": time.Now().UTC()})
}

func (s *Server) Handler() http.Handler {
	return s.middleware(s.mux)
}

func (s *Server) middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started := time.Now()
		requestID := r.Header.Get("X-Request-ID")
		if requestID == "" {
			requestID = newID("req")
		}
		w.Header().Set("X-Request-ID", requestID)
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Access-Control-Allow-Origin", "http://localhost:3000")
		w.Header().Set("Vary", "Origin")
		if r.Method == http.MethodOptions {
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Idempotency-Key, If-None-Match, X-Request-ID")
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
			w.WriteHeader(http.StatusNoContent)
			return
		}
		s.requests.Add(1)
		next.ServeHTTP(w, r)
		elapsed := time.Since(started)
		s.durationMS.Add(uint64(elapsed.Milliseconds()))
		s.logger.Info("request complete", "request_id", requestID, "method", r.Method, "path", r.URL.Path, "elapsed_ms", elapsed.Milliseconds())
	})
}

func (s *Server) health(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "time": time.Now().UTC()})
}

func (s *Server) providers(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"providers": s.service.ProviderStatuses()})
}

func (s *Server) metrics(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; version=0.0.4")
	_, _ = fmt.Fprintf(w, "# TYPE centerseat_http_requests_total counter\ncenterseat_http_requests_total %d\n", s.requests.Load())
	_, _ = fmt.Fprintf(w, "# TYPE centerseat_http_errors_total counter\ncenterseat_http_errors_total %d\n", s.errors.Load())
	_, _ = fmt.Fprintf(w, "# TYPE centerseat_http_request_duration_milliseconds_total counter\ncenterseat_http_request_duration_milliseconds_total %d\n", s.durationMS.Load())
}

func (s *Server) createQuery(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
	if err != nil {
		s.problem(w, r, http.StatusBadRequest, "Invalid request body", err.Error())
		return
	}
	var request domain.QueryRequest
	decoder := json.NewDecoder(strings.NewReader(string(body)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		s.problem(w, r, http.StatusBadRequest, "Invalid JSON", err.Error())
		return
	}
	request.SetDefaults()
	if err := request.Validate(); err != nil {
		s.problem(w, r, http.StatusUnprocessableEntity, "Query validation failed", err.Error())
		return
	}
	canonical, _ := json.Marshal(request)
	hash := sha256.Sum256(canonical)
	bodyHash := hex.EncodeToString(hash[:])
	rawKey := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if rawKey != "" && (len(rawKey) < 8 || len(rawKey) > 128) {
		s.problem(w, r, http.StatusBadRequest, "Invalid Idempotency-Key", "Idempotency-Key must be 8 to 128 characters")
		return
	}
	key := ""
	if rawKey != "" {
		key = "seat:" + rawKey
	}
	if key != "" {
		s.mu.RLock()
		queryID, found := s.byKey[key]
		cached, cachedFound := s.results[queryID]
		s.mu.RUnlock()
		if found && cachedFound {
			if cached.bodyHash != bodyHash {
				s.problem(w, r, http.StatusConflict, "Idempotency conflict", "This key was already used with a different request body")
				return
			}
			serveCached(w, cached)
			return
		}
	}
	queryID := newID("qry")
	response, err := s.service.Query(r.Context(), queryID, request)
	if err != nil {
		s.problem(w, r, http.StatusServiceUnavailable, "Seat query failed", err.Error())
		return
	}
	encoded, _ := json.Marshal(response)
	etagHash := sha256.Sum256(encoded)
	cached := cachedResult{bodyHash: bodyHash, body: encoded, etag: `"` + hex.EncodeToString(etagHash[:12]) + `"`, expires: response.ExpiresAt}
	s.mu.Lock()
	s.results[queryID] = cached
	if key != "" {
		s.byKey[key] = queryID
	}
	s.mu.Unlock()
	serveCached(w, cached)
}

func (s *Server) createShowtimeQuery(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
	if err != nil {
		s.problem(w, r, http.StatusBadRequest, "Invalid request body", err.Error())
		return
	}
	var request domain.QueryRequest
	decoder := json.NewDecoder(strings.NewReader(string(body)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		s.problem(w, r, http.StatusBadRequest, "Invalid JSON", err.Error())
		return
	}
	request.SetDefaults()
	if err := request.Validate(); err != nil {
		s.problem(w, r, http.StatusUnprocessableEntity, "Query validation failed", err.Error())
		return
	}
	canonical, _ := json.Marshal(request)
	hash := sha256.Sum256(canonical)
	bodyHash := hex.EncodeToString(hash[:])
	rawKey := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if rawKey != "" && (len(rawKey) < 8 || len(rawKey) > 128) {
		s.problem(w, r, http.StatusBadRequest, "Invalid Idempotency-Key", "Idempotency-Key must be 8 to 128 characters")
		return
	}
	key := ""
	if rawKey != "" {
		key = "showtime:" + rawKey
	}
	if key != "" {
		s.mu.RLock()
		queryID, found := s.byKey[key]
		cached, cachedFound := s.results[queryID]
		s.mu.RUnlock()
		if found && cachedFound {
			if cached.bodyHash != bodyHash {
				s.problem(w, r, http.StatusConflict, "Idempotency conflict", "This key was already used with a different request body")
				return
			}
			serveCached(w, cached)
			return
		}
	}
	queryID := newID("stq")
	response, err := s.service.Showtimes(r.Context(), queryID, request)
	if err != nil {
		s.problem(w, r, http.StatusServiceUnavailable, "Showtime query failed", err.Error())
		return
	}
	encoded, _ := json.Marshal(response)
	etagHash := sha256.Sum256(encoded)
	cached := cachedResult{bodyHash: bodyHash, body: encoded, etag: `"` + hex.EncodeToString(etagHash[:12]) + `"`, expires: response.ExpiresAt}
	s.mu.Lock()
	s.results[queryID] = cached
	if key != "" {
		s.byKey[key] = queryID
	}
	s.mu.Unlock()
	serveCached(w, cached)
}

func (s *Server) getQuery(w http.ResponseWriter, r *http.Request) {
	queryID := r.PathValue("query_id")
	if !strings.HasPrefix(queryID, "qry_") {
		s.problem(w, r, http.StatusNotFound, "Query not found", "The query ID is unknown or has expired")
		return
	}
	s.mu.RLock()
	cached, ok := s.results[queryID]
	s.mu.RUnlock()
	if !ok {
		s.problem(w, r, http.StatusNotFound, "Query not found", "The query ID is unknown or has expired")
		return
	}
	if r.Header.Get("If-None-Match") == cached.etag {
		w.Header().Set("ETag", cached.etag)
		w.WriteHeader(http.StatusNotModified)
		return
	}
	serveCached(w, cached)
}

func serveCached(w http.ResponseWriter, cached cachedResult) {
	maxAge := int(time.Until(cached.expires).Seconds())
	if maxAge < 0 {
		maxAge = 0
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", fmt.Sprintf("private, max-age=%d, must-revalidate", maxAge))
	w.Header().Set("ETag", cached.etag)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(cached.body)
}

func (s *Server) problem(w http.ResponseWriter, r *http.Request, status int, title, detail string) {
	s.errors.Add(1)
	w.Header().Set("Content-Type", "application/problem+json")
	writeJSON(w, status, map[string]any{"type": "about:blank", "title": title, "status": status, "detail": detail, "instance": r.URL.Path})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	if w.Header().Get("Content-Type") == "" {
		w.Header().Set("Content-Type", "application/json")
	}
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func newID(prefix string) string {
	buffer := make([]byte, 12)
	_, _ = rand.Read(buffer)
	return prefix + "_" + hex.EncodeToString(buffer)
}
