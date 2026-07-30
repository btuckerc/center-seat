package httpapi

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"centerseat/backend/internal/domain"
	"centerseat/backend/internal/service"
	"centerseat/backend/internal/testfixtures"
)

func TestCreateQueryIsIdempotentAndCacheable(t *testing.T) {
	provider := testfixtures.Provider{}
	server := New(service.New(provider, provider, 3), slog.New(slog.NewTextHandler(io.Discard, nil)))
	today := time.Now().Format(time.DateOnly)
	payload, _ := json.Marshal(domain.QueryRequest{
		MovieQuery: "Test Film", Location: domain.LocationConstraint{Query: "Charlotte", Latitude: 35.2271, Longitude: -80.8431, RadiusMiles: 25},
		Dates: domain.DateConstraint{Start: today, End: today}, TicketCount: 1, SeatProfile: "balanced", CandidateLimit: 10, MaxDistanceMiles: 25,
	})
	post := func() *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/v1/seat-queries", bytes.NewReader(payload))
		req.Header.Set("Idempotency-Key", "test-key-123")
		rec := httptest.NewRecorder()
		server.Handler().ServeHTTP(rec, req)
		return rec
	}
	first, second := post(), post()
	if first.Code != http.StatusOK {
		t.Fatalf("first status %d: %s", first.Code, first.Body.String())
	}
	if second.Code != http.StatusOK {
		t.Fatalf("second status %d", second.Code)
	}
	if first.Body.String() != second.Body.String() {
		t.Fatal("idempotent response changed")
	}
	etag := first.Header().Get("ETag")
	if etag == "" {
		t.Fatal("missing ETag")
	}
	var response domain.QueryResponse
	if err := json.Unmarshal(first.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	get := httptest.NewRequest(http.MethodGet, "/v1/seat-queries/"+response.QueryID, nil)
	get.Header.Set("If-None-Match", etag)
	rec := httptest.NewRecorder()
	server.Handler().ServeHTTP(rec, get)
	if rec.Code != http.StatusNotModified {
		t.Fatalf("expected 304, got %d", rec.Code)
	}
}

func TestCreateShowtimeQueryReturnsDiscoveryResults(t *testing.T) {
	provider := testfixtures.Provider{}
	server := New(service.New(provider, provider, 3), slog.New(slog.NewTextHandler(io.Discard, nil)))
	today := time.Now().Format(time.DateOnly)
	payload, _ := json.Marshal(domain.QueryRequest{
		MovieQuery: "Test Film", Location: domain.LocationConstraint{Query: "Charlotte", Latitude: 35.2271, Longitude: -80.8431, RadiusMiles: 25},
		Dates: domain.DateConstraint{Start: today, End: today}, TicketCount: 1, SeatProfile: "balanced", CandidateLimit: 10, MaxDistanceMiles: 25,
	})
	req := httptest.NewRequest(http.MethodPost, "/v1/showtime-queries", bytes.NewReader(payload))
	req.Header.Set("Idempotency-Key", "showtime-key-123")
	rec := httptest.NewRecorder()
	server.Handler().ServeHTTP(rec, req)
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
	provider := testfixtures.Provider{}
	server := New(service.New(provider, provider, 3), slog.New(slog.NewTextHandler(io.Discard, nil)))

	suggest := httptest.NewRequest(http.MethodGet, "/v1/movie-suggestions?q=Test&limit=6", nil)
	suggestResponse := httptest.NewRecorder()
	server.Handler().ServeHTTP(suggestResponse, suggest)
	if suggestResponse.Code != http.StatusOK || suggestResponse.Header().Get("ETag") == "" {
		t.Fatalf("suggestions status %d: %s", suggestResponse.Code, suggestResponse.Body.String())
	}

	today := time.Now().Format(time.DateOnly)
	payload, _ := json.Marshal(domain.QueryRequest{
		MovieQuery: "Test Film", MovieID: "movie-1",
		Location: domain.LocationConstraint{Query: "28202", RadiusMiles: 25},
		Dates:    domain.DateConstraint{Start: today, End: today}, TicketCount: 1,
		SeatProfile: "dead_center", CandidateLimit: 10, MaxDistanceMiles: 25,
	})
	post := httptest.NewRequest(http.MethodPost, "/v1/seat-queries", bytes.NewReader(payload))
	postResponse := httptest.NewRecorder()
	server.Handler().ServeHTTP(postResponse, post)
	if postResponse.Code != http.StatusOK {
		t.Fatalf("query status %d: %s", postResponse.Code, postResponse.Body.String())
	}
	var query domain.QueryResponse
	if err := json.Unmarshal(postResponse.Body.Bytes(), &query); err != nil {
		t.Fatal(err)
	}
	if len(query.Alternatives) == 0 || query.Alternatives[0].SeatMap != nil {
		t.Fatalf("expected compact alternatives without eager maps, got %#v", query.Alternatives)
	}
	detail := httptest.NewRequest(http.MethodGet, "/v1/seat-queries/"+query.QueryID+"/recommendations/2", nil)
	detailResponse := httptest.NewRecorder()
	server.Handler().ServeHTTP(detailResponse, detail)
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
