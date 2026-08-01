package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"centerseat/backend/internal/domain"
	"centerseat/backend/internal/testfixtures"
)

func TestQueryReturnsVerifiedWinnerAndAlternatives(t *testing.T) {
	provider := testfixtures.Provider{}
	svc := New(provider, provider, 4)
	today := time.Now().Format(time.DateOnly)
	q := domain.QueryRequest{
		MovieQuery: "The Test Film", Location: domain.LocationConstraint{Query: "Charlotte", Latitude: 35.2271, Longitude: -80.8431, RadiusMiles: 25},
		Dates: domain.DateConstraint{Start: today, End: today}, TicketCount: 2, SeatProfile: "balanced",
		MaxDistanceMiles: 25, CandidateLimit: 12, Time: domain.TimeConstraint{Mode: "inside", Start: "17:00", End: "22:00", Timezone: "America/New_York"},
	}
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
	svc := New(provider, provider, 2)
	today := time.Now().Format(time.DateOnly)
	q := domain.QueryRequest{
		MovieQuery: "Film", Location: domain.LocationConstraint{Query: "28202", Latitude: 35.2271, Longitude: -80.8431, RadiusMiles: 25},
		Dates: domain.DateConstraint{Start: today, End: today}, TicketCount: 1, SeatProfile: "balanced", CandidateLimit: 12,
		MaxDistanceMiles: 25, Time: domain.TimeConstraint{Mode: "outside", Start: "17:00", End: "21:00"},
	}
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
	svc := New(provider, NewUnsupportedInventoryForTest(), 2)
	today := time.Now().Format(time.DateOnly)
	q := domain.QueryRequest{
		MovieQuery: "The Test Film", Location: domain.LocationConstraint{Query: "Charlotte", Latitude: 35.2271, Longitude: -80.8431, RadiusMiles: 25},
		Dates: domain.DateConstraint{Start: today, End: today}, TicketCount: 1, SeatProfile: "balanced",
		MaxDistanceMiles: 25, CandidateLimit: 12,
	}
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
	svc := New(discovery, inventory, 6)
	if svc.fanout != 2 {
		t.Fatalf("expected provider concurrency policy to cap fan-out at 2, got %d", svc.fanout)
	}
	today := time.Now().Format(time.DateOnly)
	maximum := 50.0
	result, err := svc.Query(context.Background(), "qry_diagnostics", domain.QueryRequest{
		MovieQuery: "The Test Film",
		Location: domain.LocationConstraint{
			Query: "28202", Latitude: 35.2271, Longitude: -80.8431, RadiusMiles: 25,
		},
		Dates: domain.DateConstraint{Start: today, End: today}, TicketCount: 1,
		SeatProfile: "balanced", MaxDistanceMiles: 25, CandidateLimit: 12,
		MaxTotalPrice: &maximum, AllowUnknownPrice: true,
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

func TestQueryPromotesNextCandidateWhenWinnerChangesDuringVerification(t *testing.T) {
	discovery := testfixtures.Provider{}
	svc := New(discovery, promotionInventory{base: discovery}, 4)
	today := time.Now().Format(time.DateOnly)
	result, err := svc.Query(context.Background(), "qry_promote", domain.QueryRequest{
		MovieQuery: "The Test Film",
		Location: domain.LocationConstraint{
			Query: "28202", Latitude: 35.2271, Longitude: -80.8431, RadiusMiles: 25,
		},
		Dates: domain.DateConstraint{Start: today, End: today}, TicketCount: 1,
		SeatProfile: "balanced", MaxDistanceMiles: 25, CandidateLimit: 12,
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
	svc := New(provider, provider, 8)
	result, err := svc.Query(context.Background(), "qry_adaptive_fast", adaptiveTestQuery())
	if err != nil {
		t.Fatal(err)
	}
	if result.Winner == nil || !result.Coverage.WinnerVerified {
		t.Fatalf("expected a verified winner, got %#v", result)
	}
	if result.Coverage.InventoriesChecked != adaptiveInitialCandidates {
		t.Fatalf("expected automatic search to stop after %d strong checks, got %#v", adaptiveInitialCandidates, result.Coverage)
	}
	if provider.initialReads.Load() != adaptiveInitialCandidates {
		t.Fatalf("expected %d initial provider reads, got %d", adaptiveInitialCandidates, provider.initialReads.Load())
	}
}

func TestQueryAutomaticallyExpandsAndRecoversFromStaleCandidates(t *testing.T) {
	provider := &adaptiveTestProvider{failInitialBatch: true}
	svc := New(provider, provider, 8)
	result, err := svc.Query(context.Background(), "qry_adaptive_expand", adaptiveTestQuery())
	if err != nil {
		t.Fatal(err)
	}
	if result.Winner == nil || !result.Coverage.WinnerVerified {
		t.Fatalf("expected expansion to recover a verified winner, got %#v", result)
	}
	expected := adaptiveInitialCandidates + adaptiveExpansionBatch
	if result.Coverage.InventoriesChecked != expected || result.Coverage.InventoriesFailed != adaptiveInitialCandidates {
		t.Fatalf("expected one automatic expansion after the stale batch, got %#v", result.Coverage)
	}
	if result.Status != "complete" || len(result.Warnings) != 0 {
		t.Fatalf("recovered, verified searches should stay out of the user's way: %#v", result)
	}
}

type adaptiveTestProvider struct {
	initialReads     atomic.Int32
	failInitialBatch bool
}

func (*adaptiveTestProvider) Name() string { return "adaptive-test" }

func (*adaptiveTestProvider) Supports(domain.Showtime) bool { return true }

func (*adaptiveTestProvider) MaxConcurrentInventoryReads() int { return 4 }

func (*adaptiveTestProvider) Discover(_ context.Context, query domain.QueryRequest) ([]domain.Showtime, error) {
	day, err := time.Parse(time.DateOnly, query.Dates.Start)
	if err != nil {
		return nil, err
	}
	showtimes := make([]domain.Showtime, 0, 18)
	for index := 0; index < 18; index++ {
		showtimes = append(showtimes, domain.Showtime{
			ID: fmt.Sprintf("adaptive-%02d", index), MovieTitle: query.MovieQuery,
			VenueName: "Adaptive Cinema", StartsAt: day.Add(20*time.Hour + time.Duration(index)*time.Minute),
			Format: "dolby", DistanceMiles: 1 + float64(index)/10, ReservedSeating: true,
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
	}
}

type diagnosticInventory struct {
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
