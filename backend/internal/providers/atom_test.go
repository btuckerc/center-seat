package providers

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"centerseat/backend/internal/domain"
)

func TestAtomDateRangeDiscoveryAndLiveSeatNormalization(t *testing.T) {
	var batchCalls atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("x-api-key") != "atom-test-key" {
			t.Fatalf("missing Atom API key")
		}
		if r.Header.Get("X-Atom-Partner") != "centerseat-test" {
			t.Fatalf("missing Atom partner header")
		}
		switch r.URL.Path {
		case "/partner/ping":
			w.WriteHeader(http.StatusOK)
		case "/partner/v1/venue/details/byLocation":
			supported, active := true, true
			_ = json.NewEncoder(w).Encode(atomVenueDetailsResponse{
				VenueDetails: []atomVenueDetails{{
					Venue:      atomVenue{ID: "C001", Name: "Real Cinema", Address: atomAddress{Lat: 35.2271, Lon: -80.8431}, Properties: atomVenueProperties{Supported: &supported}, IsActive: &active},
					KMDistance: 6.4,
				}},
				PageInfo: atomPageInfo{Page: 0, TotalPages: 1},
			})
		case "/partner/v1/showtime/details/forVenues":
			batchCalls.Add(1)
			var request atomShowtimesForVenuesRequest
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Fatal(err)
			}
			date := strings.TrimSuffix(request.LocalDateBounds.Start, "T00:00:00")
			_ = json.NewEncoder(w).Encode(atomShowtimesForVenuesResponse{
				VenueShowtimeDetailsMap: map[string]atomVenueShowtimeDetails{
					"C001": {ShowtimeDetails: []atomShowtimeDetail{{
						ShowtimeID: "D-" + date, ProductionID: "B001", VenueID: "C001",
						LocalShowtimeStart: date + "T19:10:00-04:00", Attributes: []string{"RESERVED", "DOLBY", "CC"},
						CheckoutURL: "https://rw-beta.atomtickets.com/checkout/redirect?showtime=" + date,
						OfferData:   atomOfferData{Offers: []atomOffer{{Label: "Adult", Price: atomPrice{Value: 12.5, CurrencyCode: "USD"}}}},
					}}},
				},
				AttributeMap: map[string]atomAttribute{
					"RESERVED": {FriendlyName: "Reserved seating"},
					"DOLBY":    {FriendlyName: "Dolby Cinema"},
					"CC":       {FriendlyName: "Closed captioning"},
				},
				ProductionDetailsMap: map[string]atomProduction{"B001": {ID: "B001", Name: "The Test Film"}},
			})
		case "/ordering/v1/discovery/auditoriums":
			seats := make([]atomSeat, 0, 9*14)
			for row := 0; row < 9; row++ {
				for column := 1; column <= 14; column++ {
					status := "AVAILABLE"
					if row == 0 && column == 1 {
						status = "OCCUPIED"
					}
					seats = append(seats, atomSeat{
						SeatID: fmt.Sprintf("%c-%d", 'A'+row, column), Row: fmt.Sprintf("%c", 'A'+row),
						Number: strconv.Itoa(column), Status: status, SeatType: "Premium Recliner",
					})
				}
			}
			_ = json.NewEncoder(w).Encode(atomAuditoriumDiscoveryResponse{
				AuditoriumID: "aud-1", ShowtimeID: r.URL.Query().Get("showtimeId"), SeatMapAvailable: true, Seats: seats,
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	provider, err := NewAtom(AtomConfig{
		BaseURL: server.URL, APIKey: "atom-test-key", PartnerID: "centerseat-test", RequestTimeout: 2 * time.Second,
	}, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	if err := provider.Check(context.Background()); err != nil {
		t.Fatal(err)
	}
	if provider.ProviderStatus("discovery").Status != "healthy" {
		t.Fatal("successful startup probe did not mark the provider healthy")
	}
	query := domain.QueryRequest{
		MovieQuery: "The Test Film",
		Location:   domain.LocationConstraint{Query: "Charlotte, NC", Latitude: 35.2271, Longitude: -80.8431, RadiusMiles: 25},
		Dates:      domain.DateConstraint{Start: "2026-07-31", End: "2026-08-08"}, TicketCount: 2, SeatProfile: "dead_center",
	}
	showtimes, err := provider.Discover(context.Background(), query)
	if err != nil {
		t.Fatal(err)
	}
	if len(showtimes) != 2 {
		t.Fatalf("expected one showtime from each seven-day batch, got %d", len(showtimes))
	}
	if batchCalls.Load() != 2 {
		t.Fatalf("expected two bounded date-range requests, got %d", batchCalls.Load())
	}
	if showtimes[0].BookingURL == "" || showtimes[0].TotalPrice == nil || *showtimes[0].TotalPrice != 25 {
		t.Fatalf("expected provider checkout URL and adult total, got %#v", showtimes[0])
	}
	inventory, err := provider.GetAvailability(context.Background(), showtimes[0], true)
	if err != nil {
		t.Fatal(err)
	}
	if len(inventory.Seats) != 126 || inventory.Confidence != "row_geometry" {
		t.Fatalf("unexpected normalized inventory: seats=%d confidence=%s", len(inventory.Seats), inventory.Confidence)
	}
	if inventory.Seats[0].Status != "sold" || inventory.Seats[0].Label != "A1" {
		t.Fatalf("occupied seat was not normalized: %#v", inventory.Seats[0])
	}
	center := inventory.Seats[4*14+6]
	if center.Label != "E7" || math.Abs(center.X-.4615) > .01 || center.Y != .5 {
		t.Fatalf("unexpected center geometry: %#v", center)
	}
}

func TestAtomRequiresAPIKeyAndHTTPS(t *testing.T) {
	if _, err := NewAtom(AtomConfig{}, nil); err == nil {
		t.Fatal("expected missing API key error")
	}
	if _, err := NewAtom(AtomConfig{BaseURL: "http://api.invalid", APIKey: "key"}, nil); err == nil {
		t.Fatal("expected non-HTTPS URL to be rejected")
	}
}

func TestAtomRequiresCoordinatesAndHonorsRadiusLimit(t *testing.T) {
	provider, err := NewAtom(AtomConfig{APIKey: "key"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	query := domain.QueryRequest{Location: domain.LocationConstraint{Query: "Charlotte", RadiusMiles: 25}}
	if _, err := provider.Discover(context.Background(), query); err == nil || !strings.Contains(err.Error(), "latitude and longitude") {
		t.Fatalf("expected precise-location error, got %v", err)
	}
	query.Location.Latitude, query.Location.Longitude, query.Location.RadiusMiles = 35, -80, 60
	if _, err := provider.Discover(context.Background(), query); err == nil || !strings.Contains(err.Error(), "maximum search radius") {
		t.Fatalf("expected radius limit error, got %v", err)
	}
}
