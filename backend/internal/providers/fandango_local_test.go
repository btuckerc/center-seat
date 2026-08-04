package providers

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"centerseat/backend/internal/domain"
)

func TestFandangoLocalReadOnlyDiscoveryAndInventory(t *testing.T) {
	var seatMapReads atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Fatalf("read-only provider sent %s", r.Method)
		}
		if r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" {
			t.Fatal("read-only provider sent account or authorization state")
		}
		switch r.URL.Path {
		case "/napi/home/autocompleteDesktopSearch":
			if r.URL.Query().Get("search") == "" {
				t.Fatal("autocomplete request omitted search")
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"resultsByType": map[string]any{
					"movies": map[string]any{
						"items": []map[string]any{
							{"id": "111", "name": "A Different Film", "link": "/a-different-film-2026-111/movie-overview"},
							{"id": "243819", "name": "Spider-Man: Brand New Day", "link": "/spider-man-brand-new-day-2026-243819/movie-overview", "releaseDate": "2026-07-31"},
						},
					},
				},
			})
		case "/napi/theaterShowtimeGroupings/243819/2026-08-01":
			assertFandangoLocationQuery(t, r)
			writeFandangoGrouping(t, w, "501", "hash-one", "2026-08-01T23:30:00Z")
		case "/napi/theaterShowtimeGroupings/243819/2026-08-02":
			assertFandangoLocationQuery(t, r)
			writeFandangoGrouping(t, w, "502", "hash-two", "2026-08-02T22:00:00Z")
		case "/napi/seatMap/hash-one", "/napi/seatMap/hash-two":
			seatMapReads.Add(1)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"totalWidth": 300, "totalHeight": 200,
				"areas": []map[string]any{{
					"ticketInfo": []map[string]any{
						{"desc": "Child", "price": "$9.00", "fee": "$1.00"},
						{"desc": "Adult", "price": "$15.00", "fee": "$2.50"},
					},
				}},
				"seats": []map[string]any{
					{"id": "A1", "row": 1, "column": 1, "x": 0, "y": 0, "width": 20, "height": 20, "type": "standard", "status": "R"},
					{"id": "A2", "row": 1, "column": 2, "x": 100, "y": 0, "width": 20, "height": 20, "type": "standard", "status": "A"},
					{"id": "A3", "row": 1, "column": 3, "x": 200, "y": 0, "width": 20, "height": 20, "type": "companion", "status": "A"},
					{"id": "B1", "row": 2, "column": 1, "x": 0, "y": 100, "width": 20, "height": 20, "type": "wheelchair", "status": "H"},
					{"id": "B2", "row": 2, "column": 2, "x": 100, "y": 100, "width": 20, "height": 20, "type": "standard", "status": "A"},
					{"id": "B3", "row": 2, "column": 3, "x": 200, "y": 100, "width": 20, "height": 20, "type": "mystery", "status": "X"},
				},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	provider, err := NewFandangoLocal(FandangoLocalConfig{
		BaseURL: server.URL, RequestTimeout: time.Second, MinimumDelay: time.Millisecond, MaxConcurrency: 2,
	}, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	if err := provider.Check(context.Background()); err != nil {
		t.Fatal(err)
	}
	suggestions, err := provider.SuggestMovies(context.Background(), "spiderman brand", 6)
	if err != nil || len(suggestions) == 0 || suggestions[0].ID != "243819" || suggestions[0].Year != "2026" {
		t.Fatalf("canonical suggestions were not ranked and normalized: %#v, %v", suggestions, err)
	}
	query := domain.QueryRequest{
		MovieQuery: "spiderman brand new day",
		Location:   domain.LocationConstraint{Query: "Charlotte, NC", Latitude: 35.2271, Longitude: -80.8431, RadiusMiles: 25},
		Dates:      domain.DateConstraint{Start: "2026-08-01", End: "2026-08-02"},
		Time:       domain.TimeConstraint{Timezone: "America/New_York"},
	}
	showtimes, err := provider.Discover(context.Background(), query)
	if err != nil {
		t.Fatal(err)
	}
	if len(showtimes) != 2 {
		t.Fatalf("expected two live showtimes, got %#v", showtimes)
	}
	first := showtimes[0]
	if first.MovieTitle != "Spider-Man: Brand New Day" || first.Format != "imax" || first.Captions != "closed" {
		t.Fatalf("unexpected discovery normalization: %#v", first)
	}
	if !first.ReservedSeating || first.InventoryProvider != fandangoProviderName || first.SeatLayoutID == "" {
		t.Fatalf("reserved seat-map capability was not preserved: %#v", first)
	}
	if first.BookingURL != "https://tickets.fandango.com/mobileexpress/checkout" {
		t.Fatalf("provider checkout URL was not preserved: %q", first.BookingURL)
	}

	inventory, err := provider.GetAvailability(context.Background(), first, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(inventory.Seats) != 6 || inventory.Confidence != "exact_coordinates" {
		t.Fatalf("unexpected inventory: %#v", inventory)
	}
	if inventory.Seats[1].X != 0.5 || inventory.Seats[1].Y != 0 || inventory.Seats[4].Y != 1 {
		t.Fatalf("seat coordinates were not normalized against the global seat bounds: %#v", inventory.Seats)
	}
	if inventory.Seats[0].Status != "sold" || inventory.Seats[1].Status != "available" ||
		inventory.Seats[3].Status != "held" || inventory.Seats[5].Status != "house" {
		t.Fatalf("unknown or observed states were not normalized fail-closed: %#v", inventory.Seats)
	}
	if inventory.TicketPrice == nil || *inventory.TicketPrice != 15 ||
		inventory.TicketFee == nil || *inventory.TicketFee != 2.5 || inventory.Currency != "USD" {
		t.Fatalf("adult price and fee were not normalized: %#v", inventory)
	}
	if _, err := provider.GetAvailability(context.Background(), first, false); err != nil {
		t.Fatal(err)
	}
	if seatMapReads.Load() != 1 {
		t.Fatalf("fresh inventory was not cached, reads=%d", seatMapReads.Load())
	}
	if _, err := provider.GetAvailability(context.Background(), first, true); err != nil {
		t.Fatal(err)
	}
	if seatMapReads.Load() != 2 {
		t.Fatalf("final verification did not bypass the cache, reads=%d", seatMapReads.Load())
	}
	if provider.ProviderStatus("inventory").Status != "healthy" || provider.ProviderStatus("inventory").LocationMode != "postal_or_coordinates" {
		t.Fatal("successful reads should report a healthy local provider")
	}
}

func TestFandangoLocalCoalescesConcurrentInventoryReads(t *testing.T) {
	var reads atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/napi/seatMap/shared-hash" {
			http.NotFound(w, r)
			return
		}
		reads.Add(1)
		time.Sleep(40 * time.Millisecond)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"totalWidth": 100, "totalHeight": 100,
			"seats": []map[string]any{{
				"id": "A1", "row": 1, "column": 1, "x": 0, "y": 0,
				"width": 20, "height": 20, "type": "standard", "status": "A",
			}},
		})
	}))
	defer server.Close()
	provider, err := NewFandangoLocal(FandangoLocalConfig{
		BaseURL: server.URL, RequestTimeout: time.Second, MinimumDelay: time.Millisecond, MaxConcurrency: 8,
	}, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	showtime := domain.Showtime{ID: "showtime", SeatLayoutID: "shared-hash", InventoryProvider: fandangoProviderName}
	start := make(chan struct{})
	readErrors := make(chan error, 12)
	var wait sync.WaitGroup
	for range 12 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			inventory, err := provider.GetAvailability(context.Background(), showtime, false)
			if err == nil && len(inventory.Seats) != 1 {
				err = errors.New("coalesced inventory was incomplete")
			}
			readErrors <- err
		}()
	}
	close(start)
	wait.Wait()
	close(readErrors)
	for err := range readErrors {
		if err != nil {
			t.Fatal(err)
		}
	}
	if reads.Load() != 1 {
		t.Fatalf("expected one shared upstream read, got %d", reads.Load())
	}
}

func TestFandangoFormatComesFromTheShowtimeNotTheaterAmenities(t *testing.T) {
	var response fandangoShowtimeGroupingsResponse
	err := json.Unmarshal([]byte(`{
		"theaterShowtimes":{"theaters":[{
			"id":"theater-1","name":"Multiplex with IMAX","distance":4.2,
			"amenities":[{"name":"IMAX"}],
			"variants":[
				{"filmFormatHeader":"Standard","amenityGroups":[{"hasReservedSeating":true,"showtimes":[{"id":"standard","dateUtc":"2026-08-06T18:00:00Z","showtimeHashCode":"hash-standard","ticketingJumpPageURL":"https://tickets.fandango.com/standard"}]}]},
				{"filmFormatHeader":"3D","amenityGroups":[{"hasReservedSeating":true,"showtimes":[{"id":"three-d","dateUtc":"2026-08-06T19:00:00Z","filmFormat":[{"filterName":"3D"}],"showtimeHashCode":"hash-three-d","ticketingJumpPageURL":"https://tickets.fandango.com/three-d"}]}]},
				{"filmFormatHeader":"Premium Format","amenityGroups":[{"hasReservedSeating":true,"showtimes":[{"id":"imax","dateUtc":"2026-08-06T20:00:00Z","filmFormat":[{"filterName":"IMAX"}],"showtimeHashCode":"hash-imax","ticketingJumpPageURL":"https://tickets.fandango.com/imax"}]}]}
			]
		}]}
	}`), &response)
	if err != nil {
		t.Fatal(err)
	}
	showtimes := normalizeFandangoShowtimes(response, fandangoMovie{ID: "243819", Name: "Spider-Man: Brand New Day"}, domain.QueryRequest{
		Time: domain.TimeConstraint{Timezone: "America/New_York"},
	})
	formats := map[string]string{}
	for _, showtime := range showtimes {
		formats[showtime.ID] = showtime.Format
		if !showtime.ReservedSeating || !contains(showtime.Amenities, "reserved_seating") {
			t.Fatalf("reserved-seating capability was lost: %#v", showtime)
		}
	}
	if formats["standard"] != "standard" || formats["three-d"] != "3d" || formats["imax"] != "imax" {
		t.Fatalf("theater-level IMAX availability polluted per-showtime formats: %#v", formats)
	}
}

func TestFandangoLocalSafetyBoundaries(t *testing.T) {
	if _, err := NewFandangoLocal(FandangoLocalConfig{BaseURL: "https://example.org"}, nil); err == nil {
		t.Fatal("expected a non-Fandango production host to be rejected")
	}
	for _, path := range []string{
		"/token",
		"/checkoutapi/reservations/v2",
		"/napi/seatMap/../../checkout",
		"/napi/theaterShowtimeGroupings/not-numeric/2026-08-01",
	} {
		if allowedFandangoReadPath(path) {
			t.Fatalf("unsafe path was allowlisted: %s", path)
		}
	}
	for _, path := range []string{
		"/napi/home/autocompleteDesktopSearch",
		"/napi/seatMap/hash-one",
		"/napi/theaterShowtimeGroupings/243819/2026-08-01",
	} {
		if !allowedFandangoReadPath(path) {
			t.Fatalf("expected read path to be allowlisted: %s", path)
		}
	}

	var followed atomic.Bool
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/napi/home/autocompleteDesktopSearch" {
			http.Redirect(w, r, "/checkoutapi/reservations/v2", http.StatusFound)
			return
		}
		followed.Store(true)
		w.WriteHeader(http.StatusCreated)
	}))
	defer server.Close()
	provider, err := NewFandangoLocal(FandangoLocalConfig{
		BaseURL: server.URL, RequestTimeout: time.Second, MinimumDelay: time.Millisecond,
	}, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	var response fandangoAutocompleteResponse
	if err := provider.getJSON(context.Background(), "/napi/home/autocompleteDesktopSearch", nil, &response); err == nil {
		t.Fatal("expected an allowlisted read redirect to be rejected")
	}
	if followed.Load() {
		t.Fatal("provider followed a redirect outside its GET allowlist")
	}
}

func TestFandangoLocalRetriesObservedReadFailures(t *testing.T) {
	var attempts atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if attempts.Add(1) < 3 {
			http.Error(w, "temporary", http.StatusInternalServerError)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"resultsByType": map[string]any{}})
	}))
	defer server.Close()
	provider, err := NewFandangoLocal(FandangoLocalConfig{
		BaseURL: server.URL, RequestTimeout: 2 * time.Second, MinimumDelay: time.Millisecond,
	}, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	var response fandangoAutocompleteResponse
	if err := provider.getJSON(context.Background(), "/napi/home/autocompleteDesktopSearch", nil, &response); err != nil {
		t.Fatal(err)
	}
	if attempts.Load() != 3 {
		t.Fatalf("expected two bounded retries, got %d attempts", attempts.Load())
	}
}

func TestFandangoLocalRetriesInterruptedTransportReads(t *testing.T) {
	var attempts atomic.Int32
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if attempts.Add(1) < 3 {
			return nil, errors.New("connection reset by peer")
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(`{"resultsByType":{}}`)),
			Request:    request,
		}, nil
	})}
	provider, err := NewFandangoLocal(FandangoLocalConfig{
		BaseURL: defaultFandangoBaseURL, RequestTimeout: 2 * time.Second, MinimumDelay: time.Millisecond,
	}, client)
	if err != nil {
		t.Fatal(err)
	}
	var response fandangoAutocompleteResponse
	if err := provider.getJSON(context.Background(), "/napi/home/autocompleteDesktopSearch", nil, &response); err != nil {
		t.Fatal(err)
	}
	if attempts.Load() != 3 {
		t.Fatalf("expected interrupted transport reads to receive two bounded retries, got %d attempts", attempts.Load())
	}
}

func TestFandangoLocalFailsClosedWhenAnyRequestedDateCannotBeDiscovered(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/napi/theaterShowtimeGroupings/243819/2026-08-01":
			writeFandangoGrouping(t, w, "501", "hash-one", "2026-08-01T23:30:00Z")
		case "/napi/theaterShowtimeGroupings/243819/2026-08-02":
			http.Error(w, "temporary upstream failure", http.StatusInternalServerError)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	provider, err := NewFandangoLocal(FandangoLocalConfig{
		BaseURL: server.URL, RequestTimeout: 2 * time.Second, MinimumDelay: time.Millisecond, MaxConcurrency: 2,
	}, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	_, err = provider.Discover(context.Background(), domain.QueryRequest{
		MovieQuery: "Spider-Man: Brand New Day", MovieID: "243819",
		Location: domain.LocationConstraint{Latitude: 35.2271, Longitude: -80.8431, RadiusMiles: 25},
		Dates:    domain.DateConstraint{Start: "2026-08-01", End: "2026-08-02"},
	})
	if err == nil || !strings.Contains(err.Error(), "1 of 2 Fandango date queries failed after retries") {
		t.Fatalf("expected partial date discovery to fail closed, got %v", err)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (roundTrip roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return roundTrip(request)
}

func assertFandangoLocationQuery(t *testing.T, request *http.Request) {
	t.Helper()
	query := request.URL.Query()
	if query.Get("lat") != "35.227100" || query.Get("long") != "-80.843100" {
		t.Fatalf("precise location was not sent: %s", request.URL.RawQuery)
	}
	if query.Get("isdesktop") != "true" || query.Get("isDesktopMOP") != "true" ||
		query.Get("partnerRestrictedTicketing") != "false" {
		t.Fatalf("captured read-only query shape was not preserved: %s", request.URL.RawQuery)
	}
}

func writeFandangoGrouping(t *testing.T, writer http.ResponseWriter, id, hash, startsAt string) {
	t.Helper()
	if err := json.NewEncoder(writer).Encode(map[string]any{
		"theaterShowtimes": map[string]any{
			"theaters": []map[string]any{{
				"id": "theater-1", "name": "Neighborhood Cinema", "distance": 4.2,
				"amenities": []map[string]any{{"name": "Recliners"}},
				"variants": []map[string]any{{
					"filmFormatHeader": "IMAX",
					"amenityGroups": []map[string]any{{
						"amenityString": "Closed Captions", "hasReservedSeating": true,
						"showtimes": []map[string]any{{
							"id": id, "dateUtc": startsAt, "filmFormat": []string{"IMAX"},
							"showtimeHashCode":     hash,
							"ticketingJumpPageURL": "https://tickets.fandango.com/mobileexpress/checkout",
						}},
					}},
				}},
			}},
		},
	}); err != nil {
		t.Fatal(err)
	}
}
