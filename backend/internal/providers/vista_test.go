package providers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"centerseat/backend/internal/domain"
)

func TestVistaDateRangeAndSeatNormalization(t *testing.T) {
	var authCalls atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/connect/token":
			authCalls.Add(1)
			if err := r.ParseForm(); err != nil {
				t.Fatal(err)
			}
			if r.Form.Get("grant_type") != "password" || r.Form.Get("client_id") != "centerseat-test" {
				t.Fatalf("unexpected auth form: %v", r.Form)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "token", "expires_in": 43200})
		case strings.HasPrefix(r.URL.Path, "/ocapi/v1/showtimes/by-business-date/"):
			date := strings.TrimPrefix(r.URL.Path, "/ocapi/v1/showtimes/by-business-date/")
			if r.Header.Get("Authorization") != "Bearer token" {
				t.Fatalf("missing bearer token")
			}
			_ = json.NewEncoder(w).Encode(showtimeFixture(date))
		case r.URL.Path == "/ocapi/v1/seat-layouts/layout-1":
			_ = json.NewEncoder(w).Encode(layoutFixture())
		case strings.HasPrefix(r.URL.Path, "/ocapi/v1/showtimes/") && strings.HasSuffix(r.URL.Path, "/seat-availability"):
			states := []map[string]string{}
			for index := 1; index <= 14; index++ {
				status := "Available"
				if index == 1 {
					status = "Sold"
				}
				states = append(states, map[string]string{"seatId": fmt.Sprintf("F-%d", index), "status": status})
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"seatAvailabilities": states, "isSoldOut": false})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	provider, err := NewVista(VistaConfig{
		APIBaseURL: server.URL, AuthURL: server.URL + "/connect/token", ClientID: "centerseat-test",
		Username: "user", Password: "password", RequestTimeout: 2 * time.Second,
	}, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	query := domain.QueryRequest{
		MovieQuery: "Spider-Man: Brand New Day", Location: domain.LocationConstraint{Query: "Charlotte, NC", RadiusMiles: 25},
		Dates: domain.DateConstraint{Start: "2026-07-31", End: "2026-08-01"}, TicketCount: 1, SeatProfile: "dead_center",
	}
	showtimes, err := provider.Discover(context.Background(), query)
	if err != nil {
		t.Fatal(err)
	}
	if len(showtimes) != 2 {
		t.Fatalf("expected two dates, got %d", len(showtimes))
	}
	if showtimes[0].BookingURL != "" {
		t.Fatalf("unexpected fabricated booking URL %q", showtimes[0].BookingURL)
	}
	inventory, err := provider.GetAvailability(context.Background(), showtimes[0], true)
	if err != nil {
		t.Fatal(err)
	}
	if len(inventory.Seats) != 14 {
		t.Fatalf("expected 14 seats, got %d", len(inventory.Seats))
	}
	if inventory.Seats[0].Status != "sold" {
		t.Fatalf("expected sold seat, got %s", inventory.Seats[0].Status)
	}
	if inventory.Seats[6].X < .45 || inventory.Seats[6].X > .5 {
		t.Fatalf("unexpected normalized center coordinate %f", inventory.Seats[6].X)
	}
	if authCalls.Load() != 1 {
		t.Fatalf("expected cached token, auth called %d times", authCalls.Load())
	}
}

func TestVistaRequiresCompleteHTTPSConfiguration(t *testing.T) {
	if _, err := NewVista(VistaConfig{}, nil); err == nil {
		t.Fatal("expected missing configuration error")
	}
	if _, err := NewVista(VistaConfig{APIBaseURL: "http://api.invalid", AuthURL: "http://auth.invalid", ClientID: "c", Username: "u", Password: "p"}, nil); err == nil {
		t.Fatal("expected non-HTTPS URLs to be rejected")
	}
}

func showtimeFixture(date string) map[string]any {
	return map[string]any{
		"showtimes": []map[string]any{{
			"id": "show-" + date, "schedule": map[string]string{"startsAt": date + "T19:10:00-04:00"},
			"isSoldOut": false, "seatLayoutId": "layout-1", "filmId": "film-1", "siteId": "site-1",
			"screenId": "screen-1", "attributeIds": []string{"attr-1"}, "isAllocatedSeating": true,
		}},
		"relatedData": map[string]any{
			"films": []map[string]any{{"id": "film-1", "title": map[string]string{"text": "Spider-Man: Brand New Day"}}},
			"sites": []map[string]any{{
				"id": "site-1", "name": map[string]string{"text": "Real Cinema"},
				"location":       map[string]float64{"latitude": 35.2271, "longitude": -80.8431},
				"contactDetails": map[string]any{"address": map[string]string{"city": "Charlotte", "administrativeArea": "NC"}},
			}},
			"screens":    []map[string]any{{"id": "screen-1", "name": map[string]string{"text": "Auditorium 1"}}},
			"attributes": []map[string]any{{"id": "attr-1", "shortName": map[string]string{"text": "Dolby Cinema Recliner"}}},
		},
	}
}

func layoutFixture() map[string]any {
	seats := []map[string]any{}
	for index := 1; index <= 14; index++ {
		seats = append(seats, map[string]any{
			"id": fmt.Sprintf("F-%d", index), "label": fmt.Sprintf("%d", index), "rowLabel": "F", "type": "Normal",
			"position": map[string]int{"areaNumber": 1, "columnNumber": index - 1, "rowNumber": 5},
		})
	}
	return map[string]any{"seatLayout": map[string]any{
		"id": "layout-1", "boundary": map[string]float64{"left": 0, "top": 0, "right": 14, "bottom": 10},
		"areas": []map[string]any{{
			"boundary": map[string]float64{"left": 0, "top": 0, "right": 14, "bottom": 10},
			"rows":     []map[string]any{{"number": 5, "label": "F", "seats": seats}},
		}},
	}}
}
