package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"centerseat/backend/internal/domain"
	"centerseat/backend/internal/geocode"
	"centerseat/backend/internal/service"
	"centerseat/backend/internal/testfixtures"
)

func newTestServer() *Server {
	provider := testfixtures.Provider{}
	return New(service.New(provider, provider, 3, nil), slog.New(slog.NewTextHandler(io.Discard, nil)))
}

// queryPayload targets tomorrow so fixture screenings are never excluded as already started.
func queryPayload(mutate func(*domain.QueryRequest)) []byte {
	tomorrow := time.Now().AddDate(0, 0, 1).Format(time.DateOnly)
	request := domain.QueryRequest{
		MovieQuery: "Test Film", Location: domain.LocationConstraint{Query: "Charlotte", Latitude: 35.2271, Longitude: -80.8431, RadiusMiles: 25},
		Dates: domain.DateConstraint{Start: tomorrow, End: tomorrow}, TicketCount: 1, SeatProfile: "balanced", CandidateLimit: 10, MaxDistanceMiles: 25,
		Time: domain.TimeConstraint{Timezone: "America/New_York"},
	}
	if mutate != nil {
		mutate(&request)
	}
	payload, _ := json.Marshal(request)
	return payload
}

func serve(server *Server, method, path string, body []byte, headers map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, bytes.NewReader(body))
	for name, value := range headers {
		req.Header.Set(name, value)
	}
	rec := httptest.NewRecorder()
	server.Handler().ServeHTTP(rec, req)
	return rec
}

func expectProblem(t *testing.T, rec *httptest.ResponseRecorder, status int, code string) {
	t.Helper()
	var problem struct {
		Code string `json:"code"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &problem)
	if rec.Code != status || problem.Code != code {
		t.Fatalf("expected %d %s, got %d: %s", status, code, rec.Code, rec.Body.String())
	}
}

func decodeQuery(t *testing.T, rec *httptest.ResponseRecorder) domain.QueryResponse {
	t.Helper()
	if rec.Code != http.StatusOK {
		t.Fatalf("query status %d: %s", rec.Code, rec.Body.String())
	}
	var response domain.QueryResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	return response
}

type notFoundLocationResolver struct{}

func (notFoundLocationResolver) Resolve(context.Context, string) (geocode.Place, error) {
	return geocode.Place{}, geocode.ErrNotFound
}

func TestUnresolvedTextLocationReturnsValidationFailed(t *testing.T) {
	provider := testfixtures.Provider{}
	server := New(service.New(provider, provider, 3, notFoundLocationResolver{}), slog.New(slog.NewTextHandler(io.Discard, nil)))
	payload := queryPayload(func(q *domain.QueryRequest) {
		q.Location.Latitude, q.Location.Longitude = 0, 0
	})
	for _, path := range []string{"/v1/seat-queries", "/v1/showtime-queries"} {
		rec := serve(server, http.MethodPost, path, payload, map[string]string{"Idempotency-Key": "unknown-place-1"})
		expectProblem(t, rec, http.StatusUnprocessableEntity, "validation_failed")
	}
}

type prefetchingDiscovery struct {
	testfixtures.Provider
	prefetched atomic.Int32
}

func (d *prefetchingDiscovery) PrefetchDiscovery(context.Context, domain.QueryRequest) error {
	d.prefetched.Add(1)
	return nil
}

func TestDiscoveryPrefetchWarmsValidQueriesOnly(t *testing.T) {
	discovery := &prefetchingDiscovery{}
	server := New(service.New(discovery, testfixtures.Provider{}, 3, nil), slog.New(slog.NewTextHandler(io.Discard, nil)))
	if rec := serve(server, http.MethodPost, "/v1/discovery-prefetch", queryPayload(nil), nil); rec.Code != http.StatusNoContent || rec.Body.Len() != 0 {
		t.Fatalf("expected an empty 204, got %d: %s", rec.Code, rec.Body.String())
	}
	invalid := queryPayload(func(q *domain.QueryRequest) { q.Dates.End = "not-a-date" })
	expectProblem(t, serve(server, http.MethodPost, "/v1/discovery-prefetch", invalid, nil), http.StatusUnprocessableEntity, "validation_failed")
	if got := discovery.prefetched.Load(); got != 1 {
		t.Fatalf("expected exactly the valid query to be prefetched, got %d", got)
	}
}
func TestCreateQueryIsIdempotentAndCacheable(t *testing.T) {
	server := newTestServer()
	payload := queryPayload(nil)
	key := map[string]string{"Idempotency-Key": "test-key-123"}
	first, second := serve(server, http.MethodPost, "/v1/seat-queries", payload, key), serve(server, http.MethodPost, "/v1/seat-queries", payload, key)
	response := decodeQuery(t, first)
	if second.Code != http.StatusOK || first.Body.String() != second.Body.String() {
		t.Fatalf("idempotent replay changed: %d", second.Code)
	}
	if !response.RefreshUntil.After(response.ExpiresAt) {
		t.Fatalf("refresh_until %s must outlast expires_at %s", response.RefreshUntil, response.ExpiresAt)
	}
	etag := first.Header().Get("ETag")
	rec := serve(server, http.MethodGet, "/v1/seat-queries/"+response.QueryID, nil, map[string]string{"If-None-Match": etag})
	if etag == "" || rec.Code != http.StatusNotModified {
		t.Fatalf("expected 304 for etag %q, got %d", etag, rec.Code)
	}
}

func TestIdempotencyKeyReusedWithDifferentBodyConflicts(t *testing.T) {
	server := newTestServer()
	key := map[string]string{"Idempotency-Key": "conflict-key-1"}
	decodeQuery(t, serve(server, http.MethodPost, "/v1/seat-queries", queryPayload(nil), key))
	other := queryPayload(func(q *domain.QueryRequest) { q.TicketCount = 2 })
	expectProblem(t, serve(server, http.MethodPost, "/v1/seat-queries", other, key), http.StatusConflict, "idempotency_conflict")
}

func TestExpiredSnapshotIsGoneButRanksRefreshUntilRetentionEnds(t *testing.T) {
	server := newTestServer()
	key := map[string]string{"Idempotency-Key": "expiry-key-1"}
	first := serve(server, http.MethodPost, "/v1/seat-queries", queryPayload(nil), key)
	response := decodeQuery(t, first)

	server.now = func() time.Time { return response.ExpiresAt }
	// A stale snapshot must not be revalidated into a 304.
	stale := serve(server, http.MethodGet, "/v1/seat-queries/"+response.QueryID, nil, map[string]string{"If-None-Match": first.Header().Get("ETag")})
	expectProblem(t, stale, http.StatusGone, "query_expired")
	expectProblem(t, serve(server, http.MethodPost, "/v1/seat-queries", queryPayload(nil), key), http.StatusGone, "idempotency_key_expired")
	rank := serve(server, http.MethodGet, "/v1/seat-queries/"+response.QueryID+"/recommendations/1", nil, nil)
	if rank.Code != http.StatusOK {
		t.Fatalf("rank refresh inside retention: %d %s", rank.Code, rank.Body.String())
	}

	server.now = func() time.Time { return response.RefreshUntil }
	expectProblem(t, serve(server, http.MethodGet, "/v1/seat-queries/"+response.QueryID+"/recommendations/1", nil, nil), http.StatusGone, "query_expired")

	// Any later write evicts the retained entry; the ID and key are then unknown and the key is reusable.
	decodeQuery(t, serve(server, http.MethodPost, "/v1/seat-queries", queryPayload(nil), key))
	expectProblem(t, serve(server, http.MethodGet, "/v1/seat-queries/"+response.QueryID, nil, nil), http.StatusNotFound, "not_found")
}

func TestValidationFailuresCarryCode(t *testing.T) {
	server := newTestServer()
	missingZone := queryPayload(func(q *domain.QueryRequest) { q.Time.Timezone = "" })
	expectProblem(t, serve(server, http.MethodPost, "/v1/seat-queries", missingZone, nil), http.StatusUnprocessableEntity, "validation_failed")
	expectProblem(t, serve(server, http.MethodPost, "/v1/seat-queries", []byte("{"), nil), http.StatusBadRequest, "invalid_request")
}

// gatedDiscovery blocks Discover until released and counts upstream calls.
type gatedDiscovery struct {
	testfixtures.Provider
	calls   *atomic.Int32
	release chan struct{}
}

func (g gatedDiscovery) Discover(ctx context.Context, q domain.QueryRequest) ([]domain.Showtime, error) {
	g.calls.Add(1)
	<-g.release
	return g.Provider.Discover(ctx, q)
}

func TestConcurrentRequestsWithSameKeyQueryProvidersOnce(t *testing.T) {
	provider := testfixtures.Provider{}
	discovery := gatedDiscovery{Provider: provider, calls: &atomic.Int32{}, release: make(chan struct{})}
	server := New(service.New(discovery, provider, 3, nil), slog.New(slog.NewTextHandler(io.Discard, nil)))
	payload := queryPayload(nil)
	key := map[string]string{"Idempotency-Key": "coalesce-key-1"}

	const duplicates = 4
	responses := make([]*httptest.ResponseRecorder, duplicates)
	var wg sync.WaitGroup
	for i := range duplicates {
		wg.Add(1)
		go func() {
			defer wg.Done()
			responses[i] = serve(server, http.MethodPost, "/v1/seat-queries", payload, key)
		}()
	}
	// Let every duplicate reach the in-flight wait before the first evaluation finishes.
	deadline := time.Now().Add(2 * time.Second)
	for discovery.calls.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	time.Sleep(20 * time.Millisecond)
	close(discovery.release)
	wg.Wait()

	if calls := discovery.calls.Load(); calls != 1 {
		t.Fatalf("expected one upstream discovery, got %d", calls)
	}
	for _, rec := range responses[1:] {
		if rec.Code != http.StatusOK || rec.Body.String() != responses[0].Body.String() {
			t.Fatalf("duplicate did not replay the first result: %d", rec.Code)
		}
	}
}

func TestResultCacheIsBounded(t *testing.T) {
	server := newTestServer()
	base := time.Now()
	for i := range maxCachedResults + 5 {
		server.store(newID("qry"), "", cachedResult{retainUntil: base.Add(time.Hour + time.Duration(i)*time.Second)})
	}
	server.store("qry_newest", "seat:newest-key", cachedResult{retainUntil: base.Add(2 * time.Hour)})
	if len(server.results) != maxCachedResults {
		t.Fatalf("cache holds %d entries, cap is %d", len(server.results), maxCachedResults)
	}
	if server.byKey["seat:newest-key"] != "qry_newest" {
		t.Fatal("newest entry was evicted instead of the oldest")
	}
}

func TestCreateQueryAcceptsCustomNormalizedSeatZone(t *testing.T) {
	server := newTestServer()
	zone := domain.SeatZone{MinimumX: .15, MaximumX: .85, MinimumY: .2, MaximumY: .9}
	payload := queryPayload(func(q *domain.QueryRequest) {
		q.Location = domain.LocationConstraint{Query: "Charlotte", RadiusMiles: 25}
		q.SeatProfile, q.CustomSeatZone = "custom", &zone
	})
	response := decodeQuery(t, serve(server, http.MethodPost, "/v1/seat-queries", payload, nil))
	if response.Winner.SeatMap == nil || response.Winner.SeatMap.PreferredZone == nil {
		t.Fatalf("expected custom zone in the recommendation map, got %#v", response.Winner)
	}
}

func TestCreateShowtimeQueryReturnsDiscoveryResults(t *testing.T) {
	server := newTestServer()
	rec := serve(server, http.MethodPost, "/v1/showtime-queries", queryPayload(nil), map[string]string{"Idempotency-Key": "showtime-key-123"})
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	var response domain.ShowtimeQueryResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if len(response.Showtimes) == 0 || response.QueryID == "" {
		t.Fatalf("expected live showtime results, got %#v", response)
	}
}

func TestMovieSuggestionsAndAlternativeMapsAreReadOnDemand(t *testing.T) {
	server := newTestServer()
	suggestResponse := serve(server, http.MethodGet, "/v1/movie-suggestions?q=Test&limit=6", nil, nil)
	if suggestResponse.Code != http.StatusOK || suggestResponse.Header().Get("ETag") == "" {
		t.Fatalf("suggestions status %d: %s", suggestResponse.Code, suggestResponse.Body.String())
	}

	payload := queryPayload(func(q *domain.QueryRequest) {
		q.MovieID = "movie-1"
		q.Location = domain.LocationConstraint{Query: "28202", RadiusMiles: 25}
		q.SeatProfile = "dead_center"
	})
	query := decodeQuery(t, serve(server, http.MethodPost, "/v1/seat-queries", payload, nil))
	if len(query.Alternatives) == 0 || query.Alternatives[0].SeatMap != nil {
		t.Fatalf("expected compact alternatives without eager maps, got %#v", query.Alternatives)
	}
	detailResponse := serve(server, http.MethodGet, "/v1/seat-queries/"+query.QueryID+"/recommendations/2", nil, nil)
	if detailResponse.Code != http.StatusOK {
		t.Fatalf("detail status %d: %s", detailResponse.Code, detailResponse.Body.String())
	}
	var recommendation domain.Recommendation
	if err := json.Unmarshal(detailResponse.Body.Bytes(), &recommendation); err != nil {
		t.Fatal(err)
	}
	if recommendation.Rank != 2 || recommendation.SeatMap == nil || len(recommendation.SeatMap.Seats) == 0 {
		t.Fatalf("expected refreshed alternative map, got %#v", recommendation)
	}
}

type seatsGoneInventory struct {
	testfixtures.Provider
	gone atomic.Bool
}

func (provider *seatsGoneInventory) GetAvailability(ctx context.Context, showtime domain.Showtime, final bool) (domain.Inventory, error) {
	inventory, err := provider.Provider.GetAvailability(ctx, showtime, final)
	if err == nil && final && provider.gone.Load() {
		for i := range inventory.Seats {
			inventory.Seats[i].Status = "sold"
		}
	}
	return inventory, err
}

func TestRefreshReturnsConflictWhenSeatsAreGone(t *testing.T) {
	provider := &seatsGoneInventory{}
	server := New(service.New(provider, provider, 3, nil), slog.New(slog.NewTextHandler(io.Discard, nil)))
	response := decodeQuery(t, serve(server, http.MethodPost, "/v1/seat-queries", queryPayload(nil), nil))
	provider.gone.Store(true)
	expectProblem(t, serve(server, http.MethodGet, "/v1/seat-queries/"+response.QueryID+"/recommendations/1", nil, nil), http.StatusConflict, "seats_unavailable")
}
