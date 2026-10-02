package httpapi

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"centerseat/backend/internal/domain"
	"centerseat/backend/internal/service"
)

// Retention policy: `expires` is snapshot freshness (GET and idempotent replay return 410 after it);
// `retainUntil` is how long ranks stay refreshable. Entries are evicted at retainUntil and the cache
// never exceeds maxCachedResults.
const maxCachedResults = 1000

type cachedResult struct {
	bodyHash    string
	body        []byte
	etag        string
	key         string
	expires     time.Time
	retainUntil time.Time
	request     *domain.QueryRequest
	response    *domain.QueryResponse
}

// keyFlight marks an Idempotency-Key whose query is still running; concurrent duplicates wait on done.
type keyFlight struct {
	bodyHash string
	done     chan struct{}
}

type Server struct {
	service    *service.Service
	logger     *slog.Logger
	mux        *http.ServeMux
	mu         sync.RWMutex
	results    map[string]cachedResult
	byKey      map[string]string
	flights    map[string]*keyFlight
	now        func() time.Time
	requests   atomic.Uint64
	errors     atomic.Uint64
	durationMS atomic.Uint64
}

func New(svc *service.Service, logger *slog.Logger) *Server {
	if logger == nil {
		logger = slog.Default()
	}
	s := &Server{service: svc, logger: logger, mux: http.NewServeMux(), results: map[string]cachedResult{}, byKey: map[string]string{}, flights: map[string]*keyFlight{}, now: time.Now}
	s.routes()
	return s
}

func (s *Server) routes() {
	s.mux.HandleFunc("GET /healthz", s.health)
	s.mux.HandleFunc("GET /readyz", s.ready)
	s.mux.HandleFunc("GET /metrics", s.metrics)
	s.mux.HandleFunc("GET /v1/providers", s.providers)
	s.mux.HandleFunc("GET /v1/movie-suggestions", s.movieSuggestions)
	s.mux.HandleFunc("POST /v1/showtime-queries", s.createShowtimeQuery)
	s.mux.HandleFunc("POST /v1/discovery-prefetch", s.prefetchDiscovery)
	s.mux.HandleFunc("POST /v1/seat-queries", s.createQuery)
	s.mux.HandleFunc("GET /v1/seat-queries/{query_id}", s.getQuery)
	s.mux.HandleFunc("GET /v1/seat-queries/{query_id}/recommendations/{rank}", s.getRecommendation)
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

func (s *Server) movieSuggestions(w http.ResponseWriter, r *http.Request) {
	query := strings.TrimSpace(r.URL.Query().Get("q"))
	if len(query) < 2 || len(query) > 160 {
		s.problem(w, r, http.StatusBadRequest, "invalid_request", "Invalid movie query", "q must be between 2 and 160 characters")
		return
	}
	limit := 6
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 || parsed > 10 {
			s.problem(w, r, http.StatusBadRequest, "invalid_request", "Invalid suggestion limit", "limit must be between 1 and 10")
			return
		}
		limit = parsed
	}
	suggestions, err := s.service.MovieSuggestions(r.Context(), query, limit)
	if err != nil {
		s.problem(w, r, http.StatusServiceUnavailable, "provider_unavailable", "Movie suggestions failed", err.Error())
		return
	}
	serveETagJSON(w, r, map[string]any{"suggestions": suggestions}, 300)
}

func (s *Server) metrics(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; version=0.0.4")
	_, _ = fmt.Fprintf(w, "# TYPE centerseat_http_requests_total counter\ncenterseat_http_requests_total %d\n", s.requests.Load())
	_, _ = fmt.Fprintf(w, "# TYPE centerseat_http_errors_total counter\ncenterseat_http_errors_total %d\n", s.errors.Load())
	_, _ = fmt.Fprintf(w, "# TYPE centerseat_http_request_duration_milliseconds_total counter\ncenterseat_http_request_duration_milliseconds_total %d\n", s.durationMS.Load())
}

// decodeQuery reads and validates a query body; on failure it has already written the problem response.
func (s *Server) decodeQuery(w http.ResponseWriter, r *http.Request, scope string) (request domain.QueryRequest, bodyHash, key string, ok bool) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
	if err != nil {
		s.problem(w, r, http.StatusBadRequest, "invalid_request", "Invalid request body", err.Error())
		return request, "", "", false
	}
	decoder := json.NewDecoder(strings.NewReader(string(body)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		s.problem(w, r, http.StatusBadRequest, "invalid_request", "Invalid JSON", err.Error())
		return request, "", "", false
	}
	request.SetDefaults()
	if err := request.Validate(); err != nil {
		s.problem(w, r, http.StatusUnprocessableEntity, "validation_failed", "Query validation failed", err.Error())
		return request, "", "", false
	}
	canonical, _ := json.Marshal(request)
	hash := sha256.Sum256(canonical)
	rawKey := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if rawKey != "" && (len(rawKey) < 8 || len(rawKey) > 128) {
		s.problem(w, r, http.StatusBadRequest, "invalid_request", "Invalid Idempotency-Key", "Idempotency-Key must be 8 to 128 characters")
		return request, "", "", false
	}
	if rawKey != "" {
		key = scope + ":" + rawKey
	}
	return request, hex.EncodeToString(hash[:]), key, true
}

// replayOrClaim answers a repeated Idempotency-Key from cache (or rejects it), or makes the caller the
// single evaluator for that key. handled=true means the response was written. A non-nil flight must be
// released after the result is stored so waiting duplicates replay it instead of querying providers again.
func (s *Server) replayOrClaim(w http.ResponseWriter, r *http.Request, key, bodyHash string) (flight *keyFlight, handled bool) {
	if key == "" {
		return nil, false
	}
	for {
		s.mu.Lock()
		now := s.now()
		s.evictLocked(now)
		if queryID, found := s.byKey[key]; found {
			cached := s.results[queryID]
			s.mu.Unlock()
			switch {
			case cached.bodyHash != bodyHash:
				s.problem(w, r, http.StatusConflict, "idempotency_conflict", "Idempotency conflict", "This key was already used with a different request body")
			case !now.Before(cached.expires):
				s.problem(w, r, http.StatusGone, "idempotency_key_expired", "Idempotency key expired", "The original query snapshot has expired; send a new key to run the query again")
			default:
				serveCached(w, cached, now)
			}
			return nil, true
		}
		running, waiting := s.flights[key]
		if !waiting {
			flight = &keyFlight{bodyHash: bodyHash, done: make(chan struct{})}
			s.flights[key] = flight
			s.mu.Unlock()
			return flight, false
		}
		s.mu.Unlock()
		if running.bodyHash != bodyHash {
			s.problem(w, r, http.StatusConflict, "idempotency_conflict", "Idempotency conflict", "This key is in use with a different request body")
			return nil, true
		}
		select {
		case <-running.done:
			// Loop: replay the stored result, or claim the key if the first evaluation failed.
		case <-r.Context().Done():
			return nil, true
		}
	}
}

func (s *Server) release(key string, flight *keyFlight) {
	if flight == nil {
		return
	}
	s.mu.Lock()
	delete(s.flights, key)
	s.mu.Unlock()
	close(flight.done)
}

func (s *Server) store(queryID, key string, cached cachedResult) {
	cached.key = key
	s.mu.Lock()
	defer s.mu.Unlock()
	s.evictLocked(s.now())
	for len(s.results) >= maxCachedResults {
		oldestID := ""
		for id, candidate := range s.results {
			if oldestID == "" || candidate.retainUntil.Before(s.results[oldestID].retainUntil) {
				oldestID = id
			}
		}
		s.dropLocked(oldestID)
	}
	s.results[queryID] = cached
	if key != "" {
		s.byKey[key] = queryID
	}
}

func (s *Server) evictLocked(now time.Time) {
	for id, cached := range s.results {
		if !now.Before(cached.retainUntil) {
			s.dropLocked(id)
		}
	}
}

func (s *Server) dropLocked(queryID string) {
	if key := s.results[queryID].key; key != "" {
		delete(s.byKey, key)
	}
	delete(s.results, queryID)
}

func newCachedResult(bodyHash string, encoded []byte, expires, retainUntil time.Time) cachedResult {
	etagHash := sha256.Sum256(encoded)
	return cachedResult{bodyHash: bodyHash, body: encoded, etag: `"` + hex.EncodeToString(etagHash[:12]) + `"`, expires: expires, retainUntil: retainUntil}
}

func (s *Server) createQuery(w http.ResponseWriter, r *http.Request) {
	request, bodyHash, key, ok := s.decodeQuery(w, r, "seat")
	if !ok {
		return
	}
	flight, handled := s.replayOrClaim(w, r, key, bodyHash)
	if handled {
		return
	}
	defer s.release(key, flight)
	queryID := newID("qry")
	response, err := s.service.Query(r.Context(), queryID, request)
	if err != nil {
		if errors.Is(err, service.ErrLocationNotFound) {
			s.problem(w, r, http.StatusUnprocessableEntity, "validation_failed", "Query validation failed", err.Error())
		} else {
			s.problem(w, r, http.StatusServiceUnavailable, "provider_unavailable", "Seat query failed", err.Error())
		}
		return
	}
	s.logger.Info("seat query evaluated",
		"query_id", queryID,
		"status", response.Status,
		"movie_id", request.MovieID,
		"date_start", request.Dates.Start,
		"date_end", request.Dates.End,
		"timezone", request.Time.Timezone,
		"seat_profile", request.SeatProfile,
		"ticket_count", request.TicketCount,
		"time_mode", request.Time.Mode,
		"formats", request.Formats,
		"dates_requested", response.Coverage.DatesRequested,
		"dates_with_screenings", response.Coverage.DatesWithScreenings,
		"dates_compared", response.Coverage.DatesCompared,
		"range_best_proven", response.Coverage.RangeBestProven,
		"screenings_discovered", response.Coverage.ScreeningsDiscovered,
		"candidates", response.Coverage.ScreeningsPruned,
		"live_checks_completed", response.Coverage.InventoriesFresh,
		"live_checks_failed", response.Coverage.InventoriesFailed,
		"no_eligible_block", response.Coverage.ScreeningsUnavailable,
		"price_rejected", response.Coverage.ScreeningsPriceRejected,
		"failure_reasons", response.Coverage.InventoryFailureReasons,
		"winner_verified", response.Coverage.WinnerVerified,
		"discovery_ms", response.Coverage.DiscoveryMS,
		"inventory_ms", response.Coverage.InventoryMS,
		"verification_ms", response.Coverage.VerificationMS,
	)
	encoded, _ := json.Marshal(response)
	cached := newCachedResult(bodyHash, encoded, response.ExpiresAt, response.RefreshUntil)
	cached.request, cached.response = &request, &response
	s.store(queryID, key, cached)
	serveCached(w, cached, s.now())
}

func (s *Server) createShowtimeQuery(w http.ResponseWriter, r *http.Request) {
	request, bodyHash, key, ok := s.decodeQuery(w, r, "showtime")
	if !ok {
		return
	}
	flight, handled := s.replayOrClaim(w, r, key, bodyHash)
	if handled {
		return
	}
	defer s.release(key, flight)
	queryID := newID("stq")
	response, err := s.service.Showtimes(r.Context(), queryID, request)
	if err != nil {
		if errors.Is(err, service.ErrLocationNotFound) {
			s.problem(w, r, http.StatusUnprocessableEntity, "validation_failed", "Query validation failed", err.Error())
		} else {
			s.problem(w, r, http.StatusServiceUnavailable, "provider_unavailable", "Showtime query failed", err.Error())
		}
		return
	}
	encoded, _ := json.Marshal(response)
	// Showtime results have no refreshable ranks; keep them only long enough for idempotent replay.
	cached := newCachedResult(bodyHash, encoded, response.ExpiresAt, response.ExpiresAt)
	s.store(queryID, key, cached)
	serveCached(w, cached, s.now())
}

// prefetchDiscovery warms showtime discovery for a search the user is still composing; the
// response carries no data.
func (s *Server) prefetchDiscovery(w http.ResponseWriter, r *http.Request) {
	request, _, _, ok := s.decodeQuery(w, r, "prefetch")
	if !ok {
		return
	}
	if err := s.service.PrefetchDiscovery(r.Context(), request); err != nil {
		if errors.Is(err, service.ErrLocationNotFound) {
			s.problem(w, r, http.StatusUnprocessableEntity, "validation_failed", "Query validation failed", err.Error())
		} else {
			s.problem(w, r, http.StatusServiceUnavailable, "provider_unavailable", "Discovery prefetch failed", err.Error())
		}
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) getQuery(w http.ResponseWriter, r *http.Request) {
	queryID := r.PathValue("query_id")
	s.mu.RLock()
	cached, ok := s.results[queryID]
	s.mu.RUnlock()
	now := s.now()
	if !strings.HasPrefix(queryID, "qry_") || !ok || !now.Before(cached.retainUntil) {
		s.problem(w, r, http.StatusNotFound, "not_found", "Query not found", "The query ID is unknown or has been evicted")
		return
	}
	// Expiry is checked before conditional handling: a stale snapshot never earns a 304.
	if !now.Before(cached.expires) {
		s.problem(w, r, http.StatusGone, "query_expired", "Query expired", "The result snapshot has expired; refresh a rank or run the query again")
		return
	}
	if r.Header.Get("If-None-Match") == cached.etag {
		w.Header().Set("ETag", cached.etag)
		w.WriteHeader(http.StatusNotModified)
		return
	}
	serveCached(w, cached, now)
}

func (s *Server) getRecommendation(w http.ResponseWriter, r *http.Request) {
	queryID := r.PathValue("query_id")
	rank, err := strconv.Atoi(r.PathValue("rank"))
	if !strings.HasPrefix(queryID, "qry_") || err != nil || rank < 1 || rank > 5 {
		s.problem(w, r, http.StatusNotFound, "not_found", "Recommendation not found", "The query or recommendation rank is unknown")
		return
	}
	s.mu.RLock()
	cached, ok := s.results[queryID]
	s.mu.RUnlock()
	if !ok || cached.request == nil || cached.response == nil {
		s.problem(w, r, http.StatusNotFound, "not_found", "Recommendation not found", "The query is unknown")
		return
	}
	if !s.now().Before(cached.retainUntil) {
		s.problem(w, r, http.StatusGone, "query_expired", "Query expired", "Recommendation refresh retention has expired; run the query again")
		return
	}
	var source *domain.Recommendation
	if cached.response.Winner != nil && cached.response.Winner.Rank == rank {
		copy := *cached.response.Winner
		source = &copy
	} else {
		for _, alternative := range cached.response.Alternatives {
			if alternative.Rank == rank {
				copy := alternative
				source = &copy
				break
			}
		}
	}
	if source == nil {
		s.problem(w, r, http.StatusNotFound, "not_found", "Recommendation not found", "The requested rank was not part of this query")
		return
	}
	refreshCtx, cancel := context.WithTimeout(r.Context(), 4*time.Second)
	defer cancel()
	refreshed, err := s.service.RefreshRecommendation(refreshCtx, *source, *cached.request)
	if err != nil {
		switch {
		case errors.Is(err, service.ErrScreeningStarted):
			s.problem(w, r, http.StatusConflict, "screening_started", "Screening started", err.Error())
		case errors.Is(err, service.ErrSeatsUnavailable):
			s.problem(w, r, http.StatusConflict, "seats_unavailable", "Seats unavailable", err.Error())
		case errors.Is(err, service.ErrPriceExceeded):
			s.problem(w, r, http.StatusConflict, "price_exceeded", "Price exceeded", err.Error())
		default:
			s.problem(w, r, http.StatusServiceUnavailable, "provider_unavailable", "Provider unavailable", err.Error())
		}
		return
	}
	serveETagJSON(w, r, refreshed, 0)
}

func serveCached(w http.ResponseWriter, cached cachedResult, now time.Time) {
	maxAge := int(cached.expires.Sub(now).Seconds())
	if maxAge < 0 {
		maxAge = 0
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", fmt.Sprintf("private, max-age=%d, must-revalidate", maxAge))
	w.Header().Set("ETag", cached.etag)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(cached.body)
}

func serveETagJSON(w http.ResponseWriter, r *http.Request, value any, maxAge int) {
	encoded, _ := json.Marshal(value)
	hash := sha256.Sum256(encoded)
	etag := `"` + hex.EncodeToString(hash[:12]) + `"`
	if r.Header.Get("If-None-Match") == etag {
		w.Header().Set("ETag", etag)
		w.WriteHeader(http.StatusNotModified)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", fmt.Sprintf("private, max-age=%d, must-revalidate", maxAge))
	w.Header().Set("ETag", etag)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(encoded)
}

// problem writes an RFC 9457 body; `code` is the stable machine-readable reason clients branch on.
func (s *Server) problem(w http.ResponseWriter, r *http.Request, status int, code, title, detail string) {
	s.errors.Add(1)
	w.Header().Set("Content-Type", "application/problem+json")
	writeJSON(w, status, map[string]any{"type": "about:blank", "title": title, "status": status, "code": code, "detail": detail, "instance": r.URL.Path})
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
