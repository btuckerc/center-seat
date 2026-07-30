package providers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"centerseat/backend/internal/domain"
)

func TestOpenCinemaDiscoversAndPaginatesLiveScreenings(t *testing.T) {
	var screeningRequests atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-key" {
			t.Fatalf("missing bearer API key: %q", r.Header.Get("Authorization"))
		}
		switch r.URL.Path {
		case "/api/v1/public/status":
			_ = json.NewEncoder(w).Encode(map[string]any{"status": "ok"})
		case "/api/v1/public/screenings":
			screeningRequests.Add(1)
			if r.URL.Query().Get("lat") != "35.227100" || r.URL.Query().Get("lon") != "-80.843100" {
				t.Fatalf("missing precise location: %s", r.URL.RawQuery)
			}
			if r.URL.Query().Get("title") != "The Test Film" || r.URL.Query().Get("limit") != "200" {
				t.Fatalf("missing discovery filters: %s", r.URL.RawQuery)
			}
			if r.URL.Query().Get("cursor") == "" {
				_ = json.NewEncoder(w).Encode(map[string]any{
					"screenings": []map[string]any{
						{
							"id": "scr_1", "film_title": "The Test Film", "theater_id": "venue_1", "theater_name": "Independent Cinema",
							"theater_timezone": "America/New_York", "start_time": "2026-08-02T23:00:00Z", "formats": []string{"DCP"},
							"is_sold_out": false, "distance_km": 10.0, "accessibility_features": []string{"CLOSED_CAPTIONS"},
							"checkout": map[string]any{"type": "deeplink", "url": "https://independent.example.org/tickets/1"},
						},
						{"id": "scr_wrong", "film_title": "Another Film", "theater_timezone": "America/New_York", "start_time": "2026-08-02T20:00:00Z"},
					},
					"pagination": map[string]any{"has_more": true, "next_cursor": "next-page"},
				})
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"screenings": []map[string]any{
					{
						"id": "scr_2", "film_title": "The Test Film", "theater_id": "venue_2", "theater_name": "Repertory House",
						"theater_timezone": "America/New_York", "start_time": "2026-08-03T22:30:00Z", "formats": []string{"IMAX"},
						"is_sold_out": false, "distance_km": 20.0, "accessibility_features": []string{"AUDIO_DESC"},
						"checkout": map[string]any{"type": "deeplink", "url": "https://example.com/not-accepted"},
					},
					{"id": "scr_sold", "film_title": "The Test Film", "theater_timezone": "America/New_York", "start_time": "2026-08-03T19:00:00Z", "is_sold_out": true},
				},
				"pagination": map[string]any{"has_more": false, "next_cursor": nil},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	provider, err := NewOpenCinema(OpenCinemaConfig{BaseURL: server.URL, APIKey: "test-key", RequestTimeout: time.Second}, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	if err := provider.Check(context.Background()); err != nil {
		t.Fatal(err)
	}
	query := domain.QueryRequest{
		MovieQuery: "The Test Film",
		Location:   domain.LocationConstraint{Latitude: 35.2271, Longitude: -80.8431, RadiusMiles: 25},
		Dates:      domain.DateConstraint{Start: "2026-08-02", End: "2026-08-03"},
	}
	showtimes, err := provider.Discover(context.Background(), query)
	if err != nil {
		t.Fatal(err)
	}
	if screeningRequests.Load() != 2 {
		t.Fatalf("expected two pages, got %d", screeningRequests.Load())
	}
	if len(showtimes) != 2 {
		t.Fatalf("expected two usable screenings, got %#v", showtimes)
	}
	if showtimes[0].Format != "standard" || showtimes[0].Captions != "closed" {
		t.Fatalf("unexpected first screening normalization: %#v", showtimes[0])
	}
	if showtimes[0].BookingURL != "https://independent.example.org/tickets/1" {
		t.Fatalf("expected provider checkout URL, got %q", showtimes[0].BookingURL)
	}
	if showtimes[0].ReservedSeating || showtimes[0].InventoryProvider != "" {
		t.Fatal("Open Cinema discovery must not claim seat inventory")
	}
	if showtimes[1].Format != "imax" || !showtimes[1].AudioDescription {
		t.Fatalf("unexpected second screening normalization: %#v", showtimes[1])
	}
	if showtimes[1].BookingURL != "" {
		t.Fatalf("unsafe placeholder checkout URL was accepted: %q", showtimes[1].BookingURL)
	}
	if provider.ProviderStatus("discovery").Status != "healthy" {
		t.Fatal("successful provider should report healthy")
	}
}

func TestOpenCinemaConfigurationFailsClosed(t *testing.T) {
	if _, err := NewOpenCinema(OpenCinemaConfig{}, nil); err == nil {
		t.Fatal("expected missing key error")
	}
	if _, err := NewOpenCinema(OpenCinemaConfig{BaseURL: "http://opencinema.test", APIKey: "key"}, nil); err == nil {
		t.Fatal("expected insecure URL error")
	}
}
