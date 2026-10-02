package service

import (
	"centerseat/backend/internal/domain"
	"centerseat/backend/internal/geocode"
	"centerseat/backend/internal/testfixtures"
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type locationResolverStub struct {
	place domain.ResolvedLocation
	err   error
}

func (r locationResolverStub) Resolve(context.Context, string) (geocode.Place, error) {
	return geocode.Place{Label: r.place.Label, Latitude: r.place.Latitude, Longitude: r.place.Longitude}, r.err
}

type locationDiscovery struct {
	testfixtures.Provider
	request domain.QueryRequest
}

func (d *locationDiscovery) Discover(ctx context.Context, q domain.QueryRequest) ([]domain.Showtime, error) {
	d.request = q
	return d.Provider.Discover(ctx, q)
}

func locationQuery() domain.QueryRequest {
	tomorrow := time.Now().AddDate(0, 0, 1).Format(time.DateOnly)
	return domain.QueryRequest{
		MovieQuery: "The Test Film", Location: domain.LocationConstraint{Query: "Brooklyn, NY", RadiusMiles: 25},
		Dates: domain.DateConstraint{Start: tomorrow, End: tomorrow}, TicketCount: 1, SeatProfile: "balanced",
		MaxDistanceMiles: 25, CandidateLimit: 12, Time: domain.TimeConstraint{Mode: "any", Timezone: "America/New_York"},
	}
}

func TestShowtimesResolvesTextBeforeDiscoveryAndReturnsResolvedLocation(t *testing.T) {
	discovery := &locationDiscovery{}
	place := domain.ResolvedLocation{Label: "Brooklyn, Kings County, New York", Latitude: 40.6526006, Longitude: -73.9497211}
	svc := New(discovery, NewUnsupportedInventoryForTest(), 2, locationResolverStub{place: place})
	response, err := svc.Showtimes(context.Background(), "stq_location", locationQuery())
	if err != nil {
		t.Fatal(err)
	}
	if discovery.request.Location.Query != "Brooklyn, NY" || discovery.request.Location.Latitude != place.Latitude || discovery.request.Location.Longitude != place.Longitude {
		t.Fatalf("provider received wrong location: %#v", discovery.request.Location)
	}
	if response.ResolvedLocation == nil || response.ResolvedLocation.Label != place.Label || response.ResolvedLocation.Latitude != place.Latitude {
		t.Fatalf("resolved location missing from response: %#v", response.ResolvedLocation)
	}
	queryResponse, err := svc.Query(context.Background(), "qry_location", locationQuery())
	if err != nil {
		t.Fatal(err)
	}
	if discovery.request.Location.Query != "Brooklyn, NY" || discovery.request.Location.Latitude != place.Latitude || discovery.request.Location.Longitude != place.Longitude {
		t.Fatalf("seat provider received wrong location: %#v", discovery.request.Location)
	}
	if queryResponse.ResolvedLocation == nil || queryResponse.ResolvedLocation.Label != place.Label || queryResponse.ResolvedLocation.Longitude != place.Longitude {
		t.Fatalf("seat response omitted resolved location: %#v", queryResponse.ResolvedLocation)
	}
}

func TestResolverFailureFallsBackToZIPText(t *testing.T) {
	discovery := &locationDiscovery{}
	svc := New(discovery, NewUnsupportedInventoryForTest(), 2, locationResolverStub{err: errors.New("upstream unavailable")})
	query := locationQuery()
	query.Location.Query = "Charlotte, NC 28202"
	if _, err := svc.Showtimes(context.Background(), "stq_zip_fallback", query); err != nil {
		t.Fatal(err)
	}
	if discovery.request.Location.Query != query.Location.Query || discovery.request.Location.Latitude != 0 || discovery.request.Location.Longitude != 0 {
		t.Fatalf("ZIP fallback did not preserve the text-only request: %#v", discovery.request.Location)
	}
}

func TestResolverNotFoundWithoutZIPReturnsLocationError(t *testing.T) {
	discovery := &locationDiscovery{}
	svc := New(discovery, NewUnsupportedInventoryForTest(), 2, locationResolverStub{err: geocode.ErrNotFound})
	_, err := svc.Showtimes(context.Background(), "stq_not_found", locationQuery())
	if !errors.Is(err, ErrLocationNotFound) {
		t.Fatalf("expected ErrLocationNotFound, got %v", err)
	}
	if discovery.request.MovieQuery != "" {
		t.Fatal("provider discovery ran after an unresolved location")
	}
}

func pinBeforeFixtureShowtimes(svc *Service, date, timezone string) {
	location, err := time.LoadLocation(timezone)
	if err != nil {
		panic(err)
	}
	day, err := time.ParseInLocation(time.DateOnly, date, location)
	if err != nil {
		panic(err)
	}
	svc.now = func() time.Time { return day.Add(-time.Hour) }
}

func TestOvernightTimeWindowUsesRequestedTimezone(t *testing.T) {
	location, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	window := domain.TimeConstraint{Start: "22:00", End: "02:00", Timezone: "America/New_York"}
	for _, test := range []struct {
		hour, minute int
		want         bool
	}{{23, 30, true}, {1, 0, true}, {12, 0, false}} {
		at := time.Date(2026, time.August, 1, test.hour, test.minute, 0, 0, location)
		window.Mode = "inside"
		if got := matchesTime(at, window); got != test.want {
			t.Errorf("inside at %s = %v, want %v", at, got, test.want)
		}
		window.Mode = "outside"
		if got := matchesTime(at, window); got == test.want {
			t.Errorf("outside at %s = %v, want %v", at, got, !test.want)
		}
	}
}

func TestQueryExcludesStartedAndNoticeWindowShowtimes(t *testing.T) {
	now := time.Date(2026, time.August, 1, 12, 0, 0, 0, time.UTC)
	q := domain.QueryRequest{MaxDistanceMiles: 25, MinStartNoticeMinutes: 30,
		Time: domain.TimeConstraint{Mode: "any", Timezone: "America/New_York"}}
	showtimes := []domain.Showtime{
		{ID: "started", StartsAt: now.Add(-time.Minute), DistanceMiles: 1},
		{ID: "notice", StartsAt: now.Add(20 * time.Minute), DistanceMiles: 1},
		{ID: "later", StartsAt: now.Add(31 * time.Minute), DistanceMiles: 1},
	}
	filtered := filterShowtimes(showtimes, q, false, now)
	if len(filtered) != 1 || filtered[0].ID != "later" {
		t.Fatalf("unexpected filtered showtimes: %#v", filtered)
	}
}

func TestRefreshRecommendationRejectsStartedScreening(t *testing.T) {
	provider := testfixtures.Provider{}
	svc := New(provider, provider, 1, nil)
	startsAt := time.Date(2026, time.August, 1, 18, 0, 0, 0, time.UTC)
	svc.now = func() time.Time { return startsAt.Add(time.Minute) }
	_, err := svc.RefreshRecommendation(context.Background(), domain.Recommendation{
		Showtime: domain.Showtime{StartsAt: startsAt},
	}, domain.QueryRequest{Time: domain.TimeConstraint{Timezone: "America/New_York"}})
	if !errors.Is(err, ErrScreeningStarted) {
		t.Fatalf("expected ErrScreeningStarted, got %v", err)
	}
}

func TestQueryReturnsVerifiedWinnerAndAlternatives(t *testing.T) {
	provider := testfixtures.Provider{}
	svc := New(provider, provider, 4, nil)
	today := time.Now().Format(time.DateOnly)
	q := domain.QueryRequest{
		MovieQuery: "The Test Film", Location: domain.LocationConstraint{Query: "Charlotte", Latitude: 35.2271, Longitude: -80.8431, RadiusMiles: 25},
		Dates: domain.DateConstraint{Start: today, End: today}, TicketCount: 2, SeatProfile: "balanced",
		MaxDistanceMiles: 25, CandidateLimit: 12, Time: domain.TimeConstraint{Mode: "inside", Start: "17:00", End: "22:00", Timezone: "America/New_York"},
	}
	pinBeforeFixtureShowtimes(svc, today, "America/New_York")
	result, err := svc.Query(context.Background(), "qry_test", q)
	if err != nil {
		t.Fatal(err)
	}
	if result.Winner == nil {
		t.Fatalf("expected winner, got %#v", result)
	}
	if len(result.Winner.Seats) != 2 {
		t.Fatalf("expected two seats, got %d", len(result.Winner.Seats))
	}
	if result.Coverage.InventoriesChecked < 2 {
		t.Fatalf("expected fan-out, got %#v", result.Coverage)
	}
	if result.Winner.VerifiedAt.IsZero() {
		t.Fatal("winner was not verified")
	}
	if !result.Coverage.WinnerVerified {
		t.Fatal("expected final winner verification to be reported")
	}
}

func TestOutsideTimeWindow(t *testing.T) {
	provider := testfixtures.Provider{}
	svc := New(provider, provider, 2, nil)
	today := time.Now().Format(time.DateOnly)
	q := domain.QueryRequest{
		MovieQuery: "Film", Location: domain.LocationConstraint{Query: "28202", Latitude: 35.2271, Longitude: -80.8431, RadiusMiles: 25},
		Dates: domain.DateConstraint{Start: today, End: today}, TicketCount: 1, SeatProfile: "balanced", CandidateLimit: 12,
		MaxDistanceMiles: 25, Time: domain.TimeConstraint{Mode: "outside", Start: "17:00", End: "21:00", Timezone: "America/New_York"},
	}
	pinBeforeFixtureShowtimes(svc, today, "America/New_York")
	result, err := svc.Query(context.Background(), "qry_test", q)
	if err != nil {
		t.Fatal(err)
	}
	if result.Winner == nil {
		t.Fatal("expected a winner outside the requested window")
	}
	clock := result.Winner.Showtime.StartsAt.Format("15:04")
	if clock >= "17:00" && clock <= "21:00" {
		t.Fatalf("winner %s is inside excluded window", clock)
	}
}

func TestShowtimesReturnsDiscoveryWithoutClaimingSeatInventory(t *testing.T) {
	provider := testfixtures.Provider{}
	svc := New(provider, NewUnsupportedInventoryForTest(), 2, nil)
	today := time.Now().Format(time.DateOnly)
	q := domain.QueryRequest{
		MovieQuery: "The Test Film", Location: domain.LocationConstraint{Query: "Charlotte", Latitude: 35.2271, Longitude: -80.8431, RadiusMiles: 25},
		Dates: domain.DateConstraint{Start: today, End: today}, TicketCount: 1, SeatProfile: "balanced",
		MaxDistanceMiles: 25, CandidateLimit: 12,
		Time: domain.TimeConstraint{Mode: "any", Timezone: "America/New_York"},
	}
	pinBeforeFixtureShowtimes(svc, today, "America/New_York")
	result, err := svc.Showtimes(context.Background(), "stq_test", q)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Showtimes) == 0 || result.Coverage.InventoriesChecked != 0 {
		t.Fatalf("expected discovery-only response, got %#v", result)
	}
}

func TestInventoryPricingAppliesPartyTotalAndHardLimit(t *testing.T) {
	price, fee := 15.0, 2.5
	showtime := domain.Showtime{}
	query := domain.QueryRequest{TicketCount: 2}
	priced, ok := applyInventoryPricing(showtime, domain.Inventory{
		TicketPrice: &price, TicketFee: &fee, Currency: "USD",
	}, query)
	if !ok || priced.TotalPrice == nil || *priced.TotalPrice != 35 || priced.Currency != "USD" {
		t.Fatalf("inventory price was not applied to the party total: %#v", priced)
	}
	maximum := 34.99
	query.MaxTotalPrice = &maximum
	if _, ok := applyInventoryPricing(showtime, domain.Inventory{
		TicketPrice: &price, TicketFee: &fee, Currency: "USD",
	}, query); ok {
		t.Fatal("expected live party total above the maximum to be rejected")
	}
}

func TestTimeWindowUsesRequestedTimezone(t *testing.T) {
	startsAt := time.Date(2026, time.August, 1, 23, 30, 0, 0, time.UTC)
	if !matchesTime(startsAt, domain.TimeConstraint{
		Mode: "inside", Start: "19:00", End: "20:00", Timezone: "America/New_York",
	}) {
		t.Fatal("expected 23:30 UTC to match the 19:00–20:00 New York window")
	}
}

func TestQuerySeparatesMapFailuresFromValidExclusions(t *testing.T) {
	discovery := testfixtures.Provider{}
	inventory := diagnosticInventory{base: discovery}
	svc := New(discovery, inventory, 6, nil)
	if svc.fanout != 2 {
		t.Fatalf("expected provider concurrency policy to cap fan-out at 2, got %d", svc.fanout)
	}
	today := time.Now().Format(time.DateOnly)
	maximum := 50.0
	pinBeforeFixtureShowtimes(svc, today, "America/New_York")
	result, err := svc.Query(context.Background(), "qry_diagnostics", domain.QueryRequest{
		MovieQuery: "The Test Film",
		Location: domain.LocationConstraint{
			Query: "28202", Latitude: 35.2271, Longitude: -80.8431, RadiusMiles: 25,
		},
		Dates: domain.DateConstraint{Start: today, End: today}, TicketCount: 1,
		SeatProfile: "balanced", MaxDistanceMiles: 25, CandidateLimit: 12,
		MaxTotalPrice: &maximum, AllowUnknownPrice: true,
		Time: domain.TimeConstraint{Mode: "any", Timezone: "America/New_York"},
	})
	if err != nil {
		t.Fatal(err)
	}
	coverage := result.Coverage
	if coverage.InventoriesChecked != 4 || coverage.InventoriesFresh != 3 || coverage.InventoriesFailed != 1 {
		t.Fatalf("unexpected inventory coverage: %#v", coverage)
	}
	if coverage.ScreeningsUnavailable != 1 || coverage.ScreeningsPriceRejected != 1 {
		t.Fatalf("expected normal exclusions to be counted separately: %#v", coverage)
	}
	if coverage.InventoryFailureReasons["rate_limited"] != 1 {
		t.Fatalf("expected normalized rate-limit diagnostic: %#v", coverage.InventoryFailureReasons)
	}
	if result.Winner == nil || result.Status != "partial" {
		t.Fatalf("expected a partial result from the remaining valid map: %#v", result)
	}
}

func TestQueryTreatsProviderNotAvailableAsAConclusiveExclusion(t *testing.T) {
	discovery := testfixtures.Provider{}
	svc := New(discovery, goneInventory{base: discovery}, 6, nil)
	today := time.Now().Format(time.DateOnly)
	pinBeforeFixtureShowtimes(svc, today, "America/New_York")
	result, err := svc.Query(context.Background(), "qry_gone", domain.QueryRequest{
		MovieQuery: "The Test Film",
		Location: domain.LocationConstraint{
			Query: "28202", Latitude: 35.2271, Longitude: -80.8431, RadiusMiles: 25,
		},
		Dates: domain.DateConstraint{Start: today, End: today}, TicketCount: 1,
		SeatProfile: "balanced", MaxDistanceMiles: 25, CandidateLimit: 12,
		Time: domain.TimeConstraint{Mode: "any", Timezone: "America/New_York"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Coverage.ScreeningsUnavailable != 1 || result.Coverage.InventoriesFailed != 0 {
		t.Fatalf("an explicit gone response should be an unavailable screening, not a provider failure: %#v", result.Coverage)
	}
	if !result.Coverage.RangeBestProven || result.Status != "complete" {
		t.Fatalf("a screening known to be unavailable must not weaken the range claim: %#v", result)
	}
}

func TestQueryPromotesNextCandidateWhenWinnerChangesDuringVerification(t *testing.T) {
	discovery := testfixtures.Provider{}
	svc := New(discovery, promotionInventory{base: discovery}, 4, nil)
	today := time.Now().Format(time.DateOnly)
	pinBeforeFixtureShowtimes(svc, today, "America/New_York")
	result, err := svc.Query(context.Background(), "qry_promote", domain.QueryRequest{
		MovieQuery: "The Test Film",
		Location: domain.LocationConstraint{
			Query: "28202", Latitude: 35.2271, Longitude: -80.8431, RadiusMiles: 25,
		},
		Dates: domain.DateConstraint{Start: today, End: today}, TicketCount: 1,
		SeatProfile: "balanced", MaxDistanceMiles: 25, CandidateLimit: 12,
		Time: domain.TimeConstraint{Mode: "any", Timezone: "America/New_York"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Winner == nil || result.Winner.Showtime.ID == "fixture-0" {
		t.Fatalf("expected the changed initial winner to be replaced: %#v", result.Winner)
	}
	if !result.Coverage.WinnerVerified {
		t.Fatal("expected the promoted winner to pass final verification")
	}
	if !strings.Contains(strings.Join(result.Warnings, " "), "promoted") {
		t.Fatalf("expected promotion to be explained, got %v", result.Warnings)
	}
}

func TestQueryStopsAfterAutomaticInitialCoverageWhenResultIsStrong(t *testing.T) {
	provider := &adaptiveTestProvider{}
	svc := New(provider, provider, 8, nil)
	query := adaptiveTestQuery()
	pinBeforeFixtureShowtimes(svc, query.Dates.Start, query.Time.Timezone)
	result, err := svc.Query(context.Background(), "qry_adaptive_fast", query)
	if err != nil {
		t.Fatal(err)
	}
	if result.Winner == nil || !result.Coverage.WinnerVerified {
		t.Fatalf("expected a verified winner, got %#v", result)
	}
	if !result.Coverage.RangeBestProven || result.Status != "complete" {
		t.Fatalf("expected unchecked screenings to be ruled out with a complete proven result: %#v", result)
	}
}

func TestQueryExpandsAfterStaleCandidatesAndKeepsTheRangeClaimHonest(t *testing.T) {
	provider := &adaptiveTestProvider{failInitialBatch: true}
	svc := New(provider, provider, 8, nil)
	query := adaptiveTestQuery()
	pinBeforeFixtureShowtimes(svc, query.Dates.Start, query.Time.Timezone)
	result, err := svc.Query(context.Background(), "qry_adaptive_expand", query)
	if err != nil {
		t.Fatal(err)
	}
	if result.Winner == nil || !result.Coverage.WinnerVerified {
		t.Fatalf("expected expansion to recover a verified winner, got %#v", result)
	}
	if result.Status != "partial" || result.Coverage.RangeBestProven || len(result.Warnings) == 0 {
		t.Fatalf("unread high-potential screenings must prevent a best-across-range claim: %#v", result)
	}
}

func TestQueryDoesNotStopAtAnArbitraryHighScore(t *testing.T) {
	provider := &adaptiveTestProvider{allCompetitive: true}
	svc := New(provider, provider, 8, nil)
	query := adaptiveTestQuery()
	pinBeforeFixtureShowtimes(svc, query.Dates.Start, query.Time.Timezone)
	result, err := svc.Query(context.Background(), "qry_adaptive_proof", query)
	if err != nil {
		t.Fatal(err)
	}
	if result.Coverage.InventoriesChecked != 18 {
		t.Fatalf("expected every still-competitive screening to be compared, got %#v", result.Coverage)
	}
	if !result.Coverage.RangeBestProven {
		t.Fatal("expected a proven range winner after all competitive screenings were compared")
	}
}

func TestQueryCoversEveryDateBeforeStoppingEarly(t *testing.T) {
	provider := &adaptiveTestProvider{days: 3}
	svc := New(provider, provider, 8, nil)
	query := adaptiveTestQuery()
	pinBeforeFixtureShowtimes(svc, query.Dates.Start, query.Time.Timezone)
	start, err := time.Parse(time.DateOnly, query.Dates.Start)
	if err != nil {
		t.Fatal(err)
	}
	query.Dates.End = start.AddDate(0, 0, 2).Format(time.DateOnly)
	result, err := svc.Query(context.Background(), "qry_dates", query)
	if err != nil {
		t.Fatal(err)
	}
	if result.Coverage.DatesRequested != 3 || result.Coverage.DatesWithScreenings != 3 || result.Coverage.DatesCompared != 3 {
		t.Fatalf("expected all three requested dates to be represented, got %#v", result.Coverage)
	}
	if result.Coverage.InventoriesChecked < 6 || result.Coverage.InventoriesChecked > 12 {
		t.Fatalf("expected at least two live comparisons per date before stopping, got %#v", result.Coverage)
	}
	if !result.Coverage.RangeBestProven {
		t.Fatal("expected a proven winner across all represented dates")
	}
}

func TestShowtimeOrderingRoundRobinsAcrossDates(t *testing.T) {
	query := adaptiveTestQuery()
	query.Time.Timezone = "UTC"
	start, err := time.Parse(time.DateOnly, query.Dates.Start)
	if err != nil {
		t.Fatal(err)
	}
	showtimes := make([]domain.Showtime, 0, 6)
	for date := 0; date < 3; date++ {
		for screening := 0; screening < 2; screening++ {
			showtimes = append(showtimes, domain.Showtime{
				ID:            fmt.Sprintf("date-%d-screening-%d", date, screening),
				StartsAt:      start.AddDate(0, 0, date).Add(time.Duration(18+screening) * time.Hour),
				DistanceMiles: float64(screening + 1), Format: "standard",
			})
		}
	}
	ordered := orderShowtimesAcrossDates(showtimes, query)
	for offset := 0; offset < 6; offset += 3 {
		dates := map[string]bool{}
		for _, showtime := range ordered[offset : offset+3] {
			dates[showtimeDateKey(showtime.StartsAt, query)] = true
		}
		if len(dates) != 3 {
			t.Fatalf("round %d did not represent every date: %#v", offset/3+1, ordered[offset:offset+3])
		}
	}
}

type adaptiveTestProvider struct {
	initialReads     atomic.Int32
	failInitialBatch bool
	allCompetitive   bool
	days             int
}

func (*adaptiveTestProvider) Name() string { return "adaptive-test" }

func (*adaptiveTestProvider) Supports(domain.Showtime) bool { return true }

func (*adaptiveTestProvider) MaxConcurrentInventoryReads() int { return 4 }

func (provider *adaptiveTestProvider) Discover(_ context.Context, query domain.QueryRequest) ([]domain.Showtime, error) {
	day, err := time.Parse(time.DateOnly, query.Dates.Start)
	if err != nil {
		return nil, err
	}
	showtimes := make([]domain.Showtime, 0, 18)
	days := provider.days
	if days < 1 {
		days = 1
	}
	for index := 0; index < 18; index++ {
		closeCandidates := 4
		if provider.failInitialBatch {
			closeCandidates = 8
		}
		distance := 24.0
		if provider.allCompetitive || index < closeCandidates {
			distance = 1 + float64(index)/100
		}
		showtimes = append(showtimes, domain.Showtime{
			ID: fmt.Sprintf("adaptive-%02d", index), MovieTitle: query.MovieQuery,
			VenueName: "Adaptive Cinema", StartsAt: day.AddDate(0, 0, index%days).Add(20*time.Hour + time.Duration(index/days)*time.Minute),
			Format: "dolby", DistanceMiles: distance, ReservedSeating: true,
			InventoryProvider: "test-fixture", Amenities: []string{"recliner", "reserved_seating"},
		})
	}
	return showtimes, nil
}

func (provider *adaptiveTestProvider) GetAvailability(ctx context.Context, showtime domain.Showtime, final bool) (domain.Inventory, error) {
	if !final {
		provider.initialReads.Add(1)
		if provider.failInitialBatch && showtime.ID < "adaptive-04" {
			return domain.Inventory{}, errors.New("Fandango read failed: connection reset by peer")
		}
	}
	return (testfixtures.Provider{}).GetAvailability(ctx, showtime, final)
}

func adaptiveTestQuery() domain.QueryRequest {
	today := time.Now().Format(time.DateOnly)
	return domain.QueryRequest{
		MovieQuery: "The Test Film",
		Location:   domain.LocationConstraint{Query: "28202", Latitude: 35.2271, Longitude: -80.8431, RadiusMiles: 25},
		Dates:      domain.DateConstraint{Start: today, End: today}, TicketCount: 1,
		SeatProfile: "dead_center", MaxDistanceMiles: 25, CandidateLimit: 18,
		Time: domain.TimeConstraint{Mode: "any", Timezone: "America/New_York"},
	}
}

type diagnosticInventory struct {
	base testfixtures.Provider
}

type goneInventory struct {
	base testfixtures.Provider
}

type promotionInventory struct {
	base testfixtures.Provider
}

func (promotionInventory) Name() string { return "promotion-inventory" }

func (promotionInventory) Supports(domain.Showtime) bool { return true }

func (provider promotionInventory) GetAvailability(ctx context.Context, showtime domain.Showtime, final bool) (domain.Inventory, error) {
	inventory, err := provider.base.GetAvailability(ctx, showtime, final)
	if err == nil && final && showtime.ID == "fixture-0" {
		for index := range inventory.Seats {
			inventory.Seats[index].Status = "sold"
		}
	}
	return inventory, err
}

func (diagnosticInventory) Name() string { return "diagnostic-inventory" }

func (goneInventory) Name() string { return "gone-inventory" }

func (goneInventory) Supports(domain.Showtime) bool { return true }

func (provider goneInventory) GetAvailability(ctx context.Context, showtime domain.Showtime, final bool) (domain.Inventory, error) {
	if !final && showtime.ID == "fixture-0" {
		return domain.Inventory{}, errors.New("Fandango read returned HTTP 410")
	}
	return provider.base.GetAvailability(ctx, showtime, final)
}

func (diagnosticInventory) Supports(domain.Showtime) bool { return true }

func (diagnosticInventory) MaxConcurrentInventoryReads() int { return 2 }

func (provider diagnosticInventory) GetAvailability(ctx context.Context, showtime domain.Showtime, final bool) (domain.Inventory, error) {
	if !final {
		switch showtime.ID {
		case "fixture-0":
			return domain.Inventory{}, errors.New("Fandango read returned HTTP 429")
		case "fixture-1":
			inventory, err := provider.base.GetAvailability(ctx, showtime, false)
			for index := range inventory.Seats {
				inventory.Seats[index].Status = "sold"
			}
			return inventory, err
		case "fixture-2":
			inventory, err := provider.base.GetAvailability(ctx, showtime, false)
			price := 75.0
			inventory.TicketPrice = &price
			return inventory, err
		}
	}
	return provider.base.GetAvailability(ctx, showtime, final)
}

type unsupportedInventoryForTest struct{}

func NewUnsupportedInventoryForTest() unsupportedInventoryForTest {
	return unsupportedInventoryForTest{}
}
func (unsupportedInventoryForTest) Name() string                  { return "unavailable" }
func (unsupportedInventoryForTest) Supports(domain.Showtime) bool { return false }
func (unsupportedInventoryForTest) GetAvailability(context.Context, domain.Showtime, bool) (domain.Inventory, error) {
	return domain.Inventory{}, nil
}

type scriptedDiscovery struct {
	showtimes  []domain.Showtime
	prefetched domain.QueryRequest
}

func (d *scriptedDiscovery) Name() string { return "scripted-discovery" }
func (d *scriptedDiscovery) Discover(context.Context, domain.QueryRequest) ([]domain.Showtime, error) {
	return append([]domain.Showtime(nil), d.showtimes...), nil
}
func (d *scriptedDiscovery) PrefetchDiscovery(_ context.Context, q domain.QueryRequest) error {
	d.prefetched = q
	return nil
}

type scriptedInventory struct {
	onRead func(context.Context, domain.Showtime, bool) (domain.Inventory, error)
}

func (scriptedInventory) Name() string                  { return "scripted-inventory" }
func (scriptedInventory) Supports(domain.Showtime) bool { return true }
func (p scriptedInventory) GetAvailability(ctx context.Context, showtime domain.Showtime, final bool) (domain.Inventory, error) {
	if p.onRead != nil {
		return p.onRead(ctx, showtime, final)
	}
	return (testfixtures.Provider{}).GetAvailability(ctx, showtime, final)
}

func scriptedQuery() domain.QueryRequest {
	day := time.Now().AddDate(0, 0, 1)
	date := day.Format(time.DateOnly)
	return domain.QueryRequest{
		MovieQuery: "The Test Film",
		Location:   domain.LocationConstraint{Query: "28202", Latitude: 35.2271, Longitude: -80.8431, RadiusMiles: 25},
		Dates:      domain.DateConstraint{Start: date, End: date}, TicketCount: 1,
		SeatProfile: "balanced", MaxDistanceMiles: 25, CandidateLimit: 18,
		Time: domain.TimeConstraint{Mode: "any", Timezone: "America/New_York"},
	}
}

func scriptedShowtimes(q domain.QueryRequest, count int) []domain.Showtime {
	day, _ := time.Parse(time.DateOnly, q.Dates.Start)
	showtimes := make([]domain.Showtime, count)
	for i := range showtimes {
		showtimes[i] = domain.Showtime{
			ID: fmt.Sprintf("script-%02d", i), MovieTitle: q.MovieQuery, VenueName: "Script Cinema",
			StartsAt: day.Add(20*time.Hour + time.Duration(i)*time.Minute), Format: "dolby",
			DistanceMiles: float64(i + 1), ReservedSeating: true, InventoryProvider: "test-fixture",
			Amenities: []string{"recliner", "reserved_seating"},
		}
	}
	return showtimes
}

func TestPipelineRefillsBeforeBlockedReadReturns(t *testing.T) {
	q := scriptedQuery()
	showtimes := scriptedShowtimes(q, 2)
	blocked := make(chan struct{})
	release := make(chan struct{})
	secondStarted := make(chan struct{})
	inventory := scriptedInventory{onRead: func(ctx context.Context, st domain.Showtime, final bool) (domain.Inventory, error) {
		if final {
			return (testfixtures.Provider{}).GetAvailability(ctx, st, true)
		}
		if st.ID == showtimes[0].ID {
			close(blocked)
			select {
			case <-release:
			case <-ctx.Done():
				return domain.Inventory{}, ctx.Err()
			}
		} else {
			close(secondStarted)
		}
		return (testfixtures.Provider{}).GetAvailability(ctx, st, false)
	}}
	svc := New(&scriptedDiscovery{showtimes: showtimes}, inventory, 2, nil)
	result := make(chan error, 1)
	go func() { _, err := svc.Query(context.Background(), "refill", q); result <- err }()
	<-blocked
	select {
	case <-secondStarted:
	case <-time.After(time.Second):
		close(release)
		t.Fatal("second inventory read did not start while the first was blocked")
	}
	close(release)
	if err := <-result; err != nil {
		t.Fatal(err)
	}
}

func TestPipelineCancellationDoesNotLaunchOrCountSkippedChecks(t *testing.T) {
	q := scriptedQuery()
	showtimes := scriptedShowtimes(q, 5)
	entered := make(chan struct{})
	var reads atomic.Int32
	inventory := scriptedInventory{onRead: func(ctx context.Context, _ domain.Showtime, final bool) (domain.Inventory, error) {
		if final {
			return domain.Inventory{}, errors.New("unexpected final read")
		}
		reads.Add(1)
		close(entered)
		<-ctx.Done()
		return domain.Inventory{}, ctx.Err()
	}}
	svc := New(&scriptedDiscovery{showtimes: showtimes}, inventory, 1, nil)
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() { _, err := svc.Query(ctx, "cancel", q); result <- err }()
	<-entered
	cancel()
	if err := <-result; !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled query must report cancellation instead of a degraded result, got %v", err)
	}
	if reads.Load() != 1 {
		t.Fatalf("cancellation launched further inventory reads: %d", reads.Load())
	}
}

func TestPrefetchDiscoveryResolvesTextLocation(t *testing.T) {
	discovery := &scriptedDiscovery{}
	svc := New(discovery, scriptedInventory{}, 1, locationResolverStub{
		place: domain.ResolvedLocation{Label: "Brooklyn, NY", Latitude: 40.7, Longitude: -73.9},
	})
	q := scriptedQuery()
	q.Location.Query, q.Location.Latitude, q.Location.Longitude = "Brooklyn, NY", 0, 0
	if err := svc.PrefetchDiscovery(context.Background(), q); err != nil {
		t.Fatal(err)
	}
	if discovery.prefetched.Location.Latitude != 40.7 || discovery.prefetched.Location.Longitude != -73.9 ||
		discovery.prefetched.Location.Query != "Brooklyn, NY" {
		t.Fatalf("prefetch received unresolved location: %#v", discovery.prefetched.Location)
	}
}

// standardShowtime has no format bonus, so its score bound is 100 minus the distance penalty
// (0.28 per mile at 25 miles) and the fixture map scores it 97.35 minus that penalty.
func standardShowtime(q domain.QueryRequest, id string, dayOffset, minute int, distance float64) domain.Showtime {
	day, _ := time.Parse(time.DateOnly, q.Dates.Start)
	return domain.Showtime{
		ID: id, MovieTitle: q.MovieQuery, VenueName: "Script Cinema " + id,
		StartsAt: day.AddDate(0, 0, dayOffset).Add(20*time.Hour + time.Duration(minute)*time.Minute), Format: "standard",
		DistanceMiles: distance, ReservedSeating: true, InventoryProvider: "test-fixture",
		Amenities: []string{"recliner", "reserved_seating"},
	}
}

// countingInventory serves the fixture map and counts non-final and final reads per showtime.
type countingInventory struct {
	mu     sync.Mutex
	reads  map[string]int
	finals map[string]int
	adjust func(showtime domain.Showtime, final bool, inventory *domain.Inventory) error
}

func (*countingInventory) Name() string                  { return "counting-inventory" }
func (*countingInventory) Supports(domain.Showtime) bool { return true }
func (c *countingInventory) GetAvailability(ctx context.Context, showtime domain.Showtime, final bool) (domain.Inventory, error) {
	c.mu.Lock()
	if c.reads == nil {
		c.reads, c.finals = map[string]int{}, map[string]int{}
	}
	if final {
		c.finals[showtime.ID]++
	} else {
		c.reads[showtime.ID]++
	}
	c.mu.Unlock()
	inventory, err := (testfixtures.Provider{}).GetAvailability(ctx, showtime, final)
	if err == nil && c.adjust != nil {
		err = c.adjust(showtime, final, &inventory)
	}
	return inventory, err
}

func (c *countingInventory) count(id string) (reads, finals int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.reads[id], c.finals[id]
}

func TestPipelineReadsLowerCandidatesToFillAlternatives(t *testing.T) {
	q := scriptedQuery()
	// After the leader (97.35) is read, every other bound is below it, so only the
	// winner-plus-four-alternatives envelope justifies reading them.
	showtimes := []domain.Showtime{standardShowtime(q, "leader", 0, 0, 0)}
	for i := range 6 {
		showtimes = append(showtimes, standardShowtime(q, fmt.Sprintf("low-%d", i), 0, i+1, float64(10+i)))
	}
	svc := New(&scriptedDiscovery{showtimes: showtimes}, &countingInventory{}, 1, nil)
	result, err := svc.Query(context.Background(), "alternatives", q)
	if err != nil {
		t.Fatal(err)
	}
	if result.Winner == nil || result.Winner.Showtime.ID != "leader" || len(result.Alternatives) != 4 || !result.Coverage.RangeBestProven {
		t.Fatalf("pruning shrank the winner-plus-four-alternatives result: %#v", result)
	}
}

func TestFailedReadReopensDateForCoverage(t *testing.T) {
	q := scriptedQuery()
	q.Dates.End = time.Now().AddDate(0, 0, 2).Format(time.DateOnly)
	showtimes := []domain.Showtime{standardShowtime(q, "leader", 0, 0, 0)}
	for i := range 4 {
		showtimes = append(showtimes, standardShowtime(q, fmt.Sprintf("day1-%d", i), 0, i+1, float64(i+1)))
	}
	// The second day can never beat the leader; it is read only for date coverage (two per date).
	for i := range 3 {
		showtimes = append(showtimes, standardShowtime(q, fmt.Sprintf("day2-%d", i), 1, i, float64(10+i)))
	}
	inventory := &countingInventory{adjust: func(showtime domain.Showtime, final bool, _ *domain.Inventory) error {
		if showtime.ID == "day2-0" && !final {
			return errors.New("read failed")
		}
		return nil
	}}
	svc := New(&scriptedDiscovery{showtimes: showtimes}, inventory, 1, nil)
	result, err := svc.Query(context.Background(), "date-reopen", q)
	if err != nil {
		t.Fatal(err)
	}
	if reads, _ := inventory.count("day2-2"); reads != 1 {
		t.Fatalf("a failed read did not reopen its date: day2-2 reads=%d coverage=%#v", reads, result.Coverage)
	}
	if result.Coverage.DatesCompared != 2 || result.Coverage.InventoriesFailed != 1 || !result.Coverage.RangeBestProven {
		t.Fatalf("expected both dates covered and a proven result despite the failure: %#v", result.Coverage)
	}
}

func TestEqualBoundCandidateTheTiebreakFavoursIsReadBeforeProof(t *testing.T) {
	q := scriptedQuery()
	// tie's bound (100 - 9.47*0.28 = 97.35) equals the leader's score, and it starts earlier, so
	// it could still be ranked first until it is read.
	showtimes := []domain.Showtime{standardShowtime(q, "leader", 0, 10, 0)}
	for i := range 4 {
		showtimes = append(showtimes, standardShowtime(q, fmt.Sprintf("filler-%d", i), 0, 20+i, float64(i+1)))
	}
	showtimes = append(showtimes, standardShowtime(q, "tie", 0, 0, 9.47))
	inventory := &countingInventory{}
	svc := New(&scriptedDiscovery{showtimes: showtimes}, inventory, 1, nil)
	result, err := svc.Query(context.Background(), "equal-tie", q)
	if err != nil {
		t.Fatal(err)
	}
	if reads, _ := inventory.count("tie"); reads != 1 {
		t.Fatalf("an equal-bound screening the tiebreak favours was pruned: reads=%d", reads)
	}
	if result.Winner == nil || result.Winner.Showtime.ID != "leader" || !result.Coverage.RangeBestProven {
		t.Fatalf("expected the leader to remain the proven winner: %#v", result)
	}
}

func TestVerificationDemotionResumesSkippedCandidates(t *testing.T) {
	q := scriptedQuery()
	// skipped's bound (97.31) is below the leader's 97.35 but above filler-0's 97.07: it only
	// becomes competitive when final verification demotes the leader.
	showtimes := []domain.Showtime{standardShowtime(q, "leader", 0, 0, 0)}
	for i := range 4 {
		showtimes = append(showtimes, standardShowtime(q, fmt.Sprintf("filler-%d", i), 0, i+1, float64(i+1)))
	}
	showtimes = append(showtimes, standardShowtime(q, "skipped", 0, 10, 9.6))
	inventory := &countingInventory{adjust: func(showtime domain.Showtime, final bool, inventory *domain.Inventory) error {
		if showtime.ID == "leader" && final {
			for i := range inventory.Seats {
				if inventory.Seats[i].Row != "A" {
					inventory.Seats[i].Status = "sold"
				}
			}
		}
		return nil
	}}
	svc := New(&scriptedDiscovery{showtimes: showtimes}, inventory, 1, nil)
	result, err := svc.Query(context.Background(), "verify-resume", q)
	if err != nil {
		t.Fatal(err)
	}
	if reads, _ := inventory.count("skipped"); reads != 1 {
		t.Fatalf("demotion did not resume the skipped screening: reads=%d result=%#v", reads, result)
	}
	if _, finals := inventory.count("filler-0"); result.Winner == nil || result.Winner.Showtime.ID != "filler-0" || finals != 1 {
		t.Fatalf("expected the new leader to be verified and win: finals=%d result=%#v", finals, result)
	}
	if !result.Coverage.WinnerVerified || !result.Coverage.RangeBestProven || result.Status != "complete" {
		t.Fatalf("resumed reads should keep an honest, proven result: %#v", result)
	}
}
