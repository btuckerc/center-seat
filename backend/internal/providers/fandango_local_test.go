package providers

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strconv"
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

	now := time.Date(2026, time.October, 2, 12, 0, 0, 0, time.UTC)
	provider, err := NewFandangoLocal(FandangoLocalConfig{
		BaseURL: server.URL, RequestTimeout: time.Second, MinimumDelay: time.Millisecond, MaxConcurrency: 2,
	}, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	provider.now = func() time.Time { return now }
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
	now = now.Add(time.Second)
	cached, err := provider.GetAvailability(context.Background(), first, false)
	if err != nil {
		t.Fatal(err)
	}
	if cached.ObservedAt != inventory.ObservedAt {
		t.Fatalf("cached inventory restamped observation: first=%s cached=%s", inventory.ObservedAt, cached.ObservedAt)
	}
	if seatMapReads.Load() != 1 {
		t.Fatalf("fresh inventory was not cached, reads=%d", seatMapReads.Load())
	}
	now = now.Add(time.Minute)
	refreshed, err := provider.GetAvailability(context.Background(), first, true)
	if err != nil {
		t.Fatal(err)
	}
	if refreshed.ObservedAt.Equal(inventory.ObservedAt) {
		t.Fatalf("live refresh retained cached timestamp %s", refreshed.ObservedAt)
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
func TestFandangoShowtimeUsesDateLocalOffset(t *testing.T) {
	var response fandangoShowtimeGroupingsResponse
	if err := json.Unmarshal([]byte(`{"theaterShowtimes":{"theaters":[{"id":"1","name":"Venue","variants":[{"amenityGroups":[{"showtimes":[{"dateLocal":"2026-10-02T19:45:00-06:00"}]}]}]}]}}`), &response); err != nil {
		t.Fatal(err)
	}
	showtimes := normalizeFandangoShowtimes(response, fandangoMovie{Name: "Film"}, domain.QueryRequest{Time: domain.TimeConstraint{Timezone: "America/New_York"}})
	if len(showtimes) != 1 || showtimes[0].StartsAt.UTC().Format(time.RFC3339) != "2026-10-03T01:45:00Z" || showtimes[0].VenueTimezone != "" {
		t.Fatalf("offset-bearing dateLocal was not parsed as an instant: %#v", showtimes)
	}
}

func TestFandangoDiscoveryCacheIncludesTimezoneAndIsBounded(t *testing.T) {
	var reads atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reads.Add(1)
		_ = json.NewEncoder(w).Encode(map[string]any{"theaterShowtimes": map[string]any{"theaters": []any{}}})
	}))
	defer server.Close()
	provider, err := NewFandangoLocal(FandangoLocalConfig{BaseURL: server.URL, MinimumDelay: time.Millisecond}, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	movie := fandangoMovie{ID: "1", Name: "Film"}
	query := domain.QueryRequest{Location: domain.LocationConstraint{Query: "10001"}, Time: domain.TimeConstraint{Timezone: "America/New_York"}}
	if _, err := provider.discoverDate(context.Background(), movie, "2026-08-01", query); err != nil {
		t.Fatal(err)
	}
	query.Time.Timezone = "America/Los_Angeles"
	if _, err := provider.discoverDate(context.Background(), movie, "2026-08-01", query); err != nil {
		t.Fatal(err)
	}
	if reads.Load() != 2 || len(provider.discoveryCache) != 2 {
		t.Fatalf("timezone-dependent entries were shared: reads=%d cache=%d", reads.Load(), len(provider.discoveryCache))
	}
	cache := make(map[string]cachedFandangoMovie)
	now := time.Now()
	for i := range 501 {
		cache[strconv.Itoa(i)] = cachedFandangoMovie{expiresAt: now.Add(time.Hour)}
		evictFandangoCache(cache, now, 500, func(value cachedFandangoMovie) time.Time { return value.expiresAt })
	}
	if len(cache) > 500 {
		t.Fatalf("cache exceeded cap: %d", len(cache))
	}
}

func TestFandangoTitleScoringPrefersExactTitlesAndYears(t *testing.T) {
	movies := []fandangoMovie{
		{ID: "exact", Name: "Dune: Part Two", ReleaseDate: "2024-03-01"},
		{ID: "event", Name: "Dune: Part Two Fan Event", ReleaseDate: "2024-03-01"},
		{ID: "sequel", Name: "Dune: Part Three", ReleaseDate: "2027-01-01"},
		{ID: "rerelease", Name: "Dune: Part Two", ReleaseDate: "2025-01-01"},
	}
	for _, query := range []string{"Dune: Part Two (2024)", "Dune Part Two 2024"} {
		if got := fandangoMovieScore(query, movies[0]); got <= fandangoMovieScore(query, movies[1]) ||
			got <= fandangoMovieScore(query, movies[2]) || got <= fandangoMovieScore(query, movies[3]) {
			t.Fatalf("exact title/year failed to win for %q: exact=%d event=%d sequel=%d rerelease=%d", query, got, fandangoMovieScore(query, movies[1]), fandangoMovieScore(query, movies[2]), fandangoMovieScore(query, movies[3]))
		}
	}
	if got, event := fandangoTitleScore("Dune: Part Two", movies[0].Name), fandangoTitleScore("Dune: Part Two", movies[1].Name); got <= event {
		t.Fatalf("exact title did not beat event variant: exact=%d event=%d", got, event)
	}
}

func TestFandangoDiscoverUsesSuppliedMovieID(t *testing.T) {
	var autocompleteReads atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/napi/home/autocompleteDesktopSearch":
			autocompleteReads.Add(1)
			http.Error(w, "unexpected title resolution", http.StatusInternalServerError)
		case "/napi/theaterShowtimeGroupings/123456/2026-10-03":
			writeFandangoGrouping(t, w, "501", "hash-one", "2026-10-03T23:30:00Z")
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	provider, err := NewFandangoLocal(FandangoLocalConfig{BaseURL: server.URL, MinimumDelay: time.Millisecond}, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	showtimes, err := provider.Discover(context.Background(), domain.QueryRequest{
		MovieQuery: "Dune: Part Two", MovieID: "123456",
		Location: domain.LocationConstraint{Latitude: 35.2271, Longitude: -80.8431, RadiusMiles: 25},
		Dates:    domain.DateConstraint{Start: "2026-10-03", End: "2026-10-03"},
		Time:     domain.TimeConstraint{Timezone: "America/New_York"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if autocompleteReads.Load() != 0 || len(showtimes) != 1 {
		t.Fatalf("supplied movie id was re-resolved or discovery failed: autocomplete=%d showtimes=%#v", autocompleteReads.Load(), showtimes)
	}
}
func TestFandangoDiscoveryCacheOverlaysCurrentMovieTitle(t *testing.T) {
	provider, err := NewFandangoLocal(FandangoLocalConfig{BaseURL: defaultFandangoBaseURL}, nil)
	if err != nil {
		t.Fatal(err)
	}
	query := domain.QueryRequest{Location: domain.LocationConstraint{Query: "10001"}, Time: domain.TimeConstraint{Timezone: "America/New_York"}}
	locationKey, _ := fandangoLocationParameters(query.Location)
	key := strings.Join([]string{"123", "2026-10-03", locationKey, query.Time.Timezone}, "|")
	provider.discoveryCache[key] = cachedFandangoShowtimes{
		showtimes: []domain.Showtime{{ID: "show", MovieTitle: "Prefetch spelling"}},
		expiresAt: time.Now().Add(time.Minute),
	}
	items, err := provider.discoverDate(context.Background(), fandangoMovie{ID: "123", Name: "Current title"}, "2026-10-03", query)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].MovieTitle != "Current title" {
		t.Fatalf("cache leaked the earlier caller's title: %#v", items)
	}
}

func TestFandango429RetryAfterDelaysNextStart(t *testing.T) {
	var starts atomic.Int32
	var secondStart time.Time
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if starts.Add(1) == 1 {
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		secondStart = time.Now()
		_, _ = io.WriteString(w, `{"resultsByType":{}}`)
	}))
	defer server.Close()
	provider, err := NewFandangoLocal(FandangoLocalConfig{BaseURL: server.URL, MinimumDelay: time.Millisecond}, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	var response fandangoAutocompleteResponse
	if err := provider.getJSON(context.Background(), "/napi/home/autocompleteDesktopSearch", nil, &response); err != nil {
		t.Fatal(err)
	}
	if starts.Load() != 2 || secondStart.Sub(start) < 950*time.Millisecond {
		t.Fatalf("Retry-After was not observed: starts=%d elapsed=%s", starts.Load(), secondStart.Sub(start))
	}
}
func TestFandangoGateCoversBodyRead(t *testing.T) {
	const limit = 2
	started := make(chan struct{}, limit+1)
	release := make(chan struct{}, limit+1)
	var active atomic.Int32
	var maximum atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		current := active.Add(1)
		for old := maximum.Load(); current > old && !maximum.CompareAndSwap(old, current); old = maximum.Load() {
		}
		defer active.Add(-1)
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		started <- struct{}{}
		<-release
		_, _ = io.WriteString(w, `{"resultsByType":{}}`)
	}))
	defer server.Close()
	provider, err := NewFandangoLocal(FandangoLocalConfig{BaseURL: server.URL, MinimumDelay: time.Millisecond, MaxConcurrency: limit}, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	errs := make(chan error, limit+1)
	for range limit + 1 {
		go func() {
			var response fandangoAutocompleteResponse
			errs <- provider.getJSON(context.Background(), "/napi/home/autocompleteDesktopSearch", nil, &response)
		}()
	}
	for range limit {
		select {
		case <-started:
		case <-time.After(time.Second):
			t.Fatal("request did not reach the streaming handler")
		}
	}
	select {
	case <-started:
		t.Fatal("request beyond MaxConcurrency started before a body completed")
	case <-time.After(200 * time.Millisecond):
	}
	release <- struct{}{}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("waiting request did not start after a body completed")
	}
	for range limit + 1 {
		release <- struct{}{}
	}
	for range limit + 1 {
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
	}
	if maximum.Load() > limit {
		t.Fatalf("active response bodies exceeded limit: %d", maximum.Load())
	}
}

func TestFandangoLastCancelledInventoryWaiterCancelsUpstream(t *testing.T) {
	upstreamStarted := make(chan struct{})
	upstreamCancelled := make(chan struct{})
	server := httptest.NewTLSServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		close(upstreamStarted)
		<-r.Context().Done()
		close(upstreamCancelled)
	}))
	defer server.Close()
	provider, err := NewFandangoLocal(FandangoLocalConfig{BaseURL: server.URL, RequestTimeout: time.Minute, MinimumDelay: time.Millisecond}, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := provider.GetAvailability(ctx, domain.Showtime{ID: "show", SeatLayoutID: "hash", InventoryProvider: fandangoProviderName}, false)
		done <- err
	}()
	select {
	case <-upstreamStarted:
	case <-time.After(time.Second):
		t.Fatal("upstream read did not start")
	}
	cancel()
	select {
	case <-upstreamCancelled:
	case <-time.After(time.Second):
		t.Fatal("last waiter cancellation did not cancel the upstream request")
	}
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("waiter returned %v, want context cancellation", err)
	}
}
func TestFandangoCancelledInventoryWaiterDoesNotPoisonJoiner(t *testing.T) {
	upstreamStarted := make(chan struct{})
	releaseBody := make(chan struct{}, 1)
	defer func() {
		select {
		case releaseBody <- struct{}{}:
		default:
		}
	}()
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		close(upstreamStarted)
		<-releaseBody
		_, _ = io.WriteString(w, `{"totalWidth":10,"totalHeight":10,"seats":[{"id":"A1","row":1,"column":1,"x":0,"y":0,"width":1,"height":1,"type":"standard","status":"A"}]}`)
	}))
	defer server.Close()
	provider, err := NewFandangoLocal(FandangoLocalConfig{BaseURL: server.URL, RequestTimeout: time.Second, MinimumDelay: time.Millisecond}, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	showtime := domain.Showtime{ID: "show", SeatLayoutID: "joined", InventoryProvider: fandangoProviderName}
	firstCtx, cancelFirst := context.WithCancel(context.Background())
	firstDone := make(chan error, 1)
	go func() {
		_, err := provider.GetAvailability(firstCtx, showtime, false)
		firstDone <- err
	}()
	select {
	case <-upstreamStarted:
	case <-time.After(time.Second):
		t.Fatal("upstream read did not start")
	}
	secondDone := make(chan error, 1)
	go func() {
		_, err := provider.GetAvailability(context.Background(), showtime, false)
		secondDone <- err
	}()
	joined := false
	timeout := time.NewTimer(time.Second)
	defer timeout.Stop()
	for !joined {
		provider.flightMu.Lock()
		for _, read := range provider.inventoryReads {
			joined = read.waiters == 2
		}
		provider.flightMu.Unlock()
		if joined {
			break
		}
		select {
		case <-timeout.C:
			t.Fatal("second caller did not join the shared inventory read")
		default:
			runtime.Gosched()
		}
	}
	cancelFirst()
	if err := <-firstDone; !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled waiter returned %v", err)
	}
	releaseBody <- struct{}{}
	if err := <-secondDone; err != nil {
		t.Fatalf("remaining waiter was poisoned by cancellation: %v", err)
	}
}
func TestFandangoFinalInventoryReadBypassesInFlightRead(t *testing.T) {
	var reads atomic.Int32
	firstStarted := make(chan struct{})
	releaseFirst := make(chan struct{}, 1)
	defer func() {
		select {
		case releaseFirst <- struct{}{}:
		default:
		}
	}()
	body := `{"totalWidth":10,"totalHeight":10,"seats":[{"id":"A1","row":1,"column":1,"x":0,"y":0,"width":1,"height":1,"type":"standard","status":"A"}]}`
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if reads.Add(1) == 1 {
			close(firstStarted)
			<-releaseFirst
		}
		_, _ = io.WriteString(w, body)
	}))
	defer server.Close()
	provider, err := NewFandangoLocal(FandangoLocalConfig{BaseURL: server.URL, RequestTimeout: time.Second, MinimumDelay: time.Millisecond}, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	showtime := domain.Showtime{ID: "show", SeatLayoutID: "final-bypass", InventoryProvider: fandangoProviderName}
	firstDone := make(chan error, 1)
	go func() {
		_, err := provider.GetAvailability(context.Background(), showtime, false)
		firstDone <- err
	}()
	select {
	case <-firstStarted:
	case <-time.After(time.Second):
		t.Fatal("non-final read did not start")
	}
	if _, err := provider.GetAvailability(context.Background(), showtime, true); err != nil {
		t.Fatalf("final read waited for or reused the non-final flight: %v", err)
	}
	if reads.Load() != 2 {
		t.Fatalf("final read did not make an independent upstream request: %d", reads.Load())
	}
	releaseFirst <- struct{}{}
	if err := <-firstDone; err != nil {
		t.Fatal(err)
	}
}
func TestFandangoCooldownPastCallerDeadlineFailsFast(t *testing.T) {
	provider, err := NewFandangoLocal(FandangoLocalConfig{BaseURL: defaultFandangoBaseURL, MinimumDelay: time.Millisecond}, nil)
	if err != nil {
		t.Fatal(err)
	}
	provider.setCooldown("10", 0)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	var response fandangoAutocompleteResponse
	err = provider.getJSON(ctx, "/napi/home/autocompleteDesktopSearch", nil, &response)
	if err == nil || !strings.Contains(err.Error(), "rate limit") {
		t.Fatalf("expected fast provider rate-limit error, got %v", err)
	}
}
func TestFandangoPrefetchAdmissionAndTwoReadLimit(t *testing.T) {
	started := make(chan struct{}, 4)
	release := make(chan struct{}, 4)
	var reads atomic.Int32
	var active atomic.Int32
	var maximum atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		reads.Add(1)
		current := active.Add(1)
		for old := maximum.Load(); current > old && !maximum.CompareAndSwap(old, current); old = maximum.Load() {
		}
		started <- struct{}{}
		<-release
		active.Add(-1)
		_ = json.NewEncoder(w).Encode(map[string]any{"theaterShowtimes": map[string]any{"theaters": []any{}}})
	}))
	defer server.Close()
	provider, err := NewFandangoLocal(FandangoLocalConfig{BaseURL: server.URL, RequestTimeout: time.Second, MinimumDelay: time.Millisecond}, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	query := domain.QueryRequest{
		MovieQuery: "Film", MovieID: "123",
		Location: domain.LocationConstraint{Query: "10001"},
		Dates:    domain.DateConstraint{Start: "2026-10-01", End: "2026-10-04"},
		Time:     domain.TimeConstraint{Timezone: "America/New_York"},
	}
	firstDone := make(chan error, 1)
	go func() { firstDone <- provider.PrefetchDiscovery(context.Background(), query) }()
	for range 2 {
		select {
		case <-started:
		case <-time.After(time.Second):
			t.Fatal("prefetch did not start two discovery requests")
		}
	}
	secondDone := make(chan error, 1)
	go func() { secondDone <- provider.PrefetchDiscovery(context.Background(), query) }()
	select {
	case err := <-secondDone:
		if err != nil {
			t.Fatalf("rejected concurrent prefetch returned error: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("second prefetch queued instead of returning immediately")
	}
	select {
	case <-started:
		t.Fatal("prefetch exceeded its two-request concurrency limit")
	case <-time.After(200 * time.Millisecond):
	}
	for range 2 {
		release <- struct{}{}
	}
	for range 2 {
		select {
		case <-started:
		case <-time.After(time.Second):
			t.Fatal("prefetch did not cover the remaining requested dates")
		}
	}
	for range 2 {
		release <- struct{}{}
	}
	if err := <-firstDone; err != nil {
		t.Fatal(err)
	}
	if reads.Load() != 4 || maximum.Load() > 2 {
		t.Fatalf("prefetch reads=%d max concurrent=%d; want all four dates and at most two active", reads.Load(), maximum.Load())
	}
}
func TestForegroundDiscoveryProgressesDuringPrefetch(t *testing.T) {
	blockedStarted := make(chan struct{}, 2)
	foregroundDates := make(chan struct{}, 2)
	releaseBlocked := make(chan struct{}, 2)
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "2026-10-01") || strings.HasSuffix(r.URL.Path, "2026-10-02") {
			blockedStarted <- struct{}{}
			<-releaseBlocked
		} else {
			foregroundDates <- struct{}{}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"theaterShowtimes": map[string]any{"theaters": []any{}}})
	}))
	defer server.Close()
	provider, err := NewFandangoLocal(FandangoLocalConfig{BaseURL: server.URL, RequestTimeout: 2 * time.Second, MinimumDelay: time.Millisecond}, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	query := domain.QueryRequest{
		MovieQuery: "Film", MovieID: "321",
		Location: domain.LocationConstraint{Query: "10001"},
		Dates:    domain.DateConstraint{Start: "2026-10-01", End: "2026-10-04"},
		Time:     domain.TimeConstraint{Timezone: "America/New_York"},
	}
	prefetchDone := make(chan error, 1)
	go func() { prefetchDone <- provider.PrefetchDiscovery(context.Background(), query) }()
	for range 2 {
		select {
		case <-blockedStarted:
		case <-time.After(time.Second):
			t.Fatal("prefetch did not occupy both speculative workers")
		}
	}
	foregroundDone := make(chan error, 1)
	go func() {
		_, err := provider.Discover(context.Background(), query)
		foregroundDone <- err
	}()
	for range 2 {
		select {
		case <-foregroundDates:
		case <-time.After(time.Second):
			t.Fatal("foreground discovery waited behind speculative worker cap")
		}
	}
	select {
	case err := <-foregroundDone:
		t.Fatalf("foreground completed despite its two joined date flights being blocked: %v", err)
	default:
	}
	for range 2 {
		releaseBlocked <- struct{}{}
	}
	if err := <-foregroundDone; err != nil {
		t.Fatal(err)
	}
	if err := <-prefetchDone; err != nil {
		t.Fatal(err)
	}
}
