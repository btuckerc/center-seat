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
