package service

import (
	"context"
	"testing"
	"time"

	"centerseat/backend/internal/domain"
	"centerseat/backend/internal/providers"
)

func TestQueryReturnsVerifiedWinnerAndAlternatives(t *testing.T) {
	provider := providers.Demo{}
	svc := New(provider, provider, 4)
	today := time.Now().Format(time.DateOnly)
	q := domain.QueryRequest{
		MovieQuery: "The Test Film", Location: domain.LocationConstraint{Query: "Charlotte", RadiusMiles: 25},
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
}

func TestOutsideTimeWindow(t *testing.T) {
	provider := providers.Demo{}
	svc := New(provider, provider, 2)
	today := time.Now().Format(time.DateOnly)
	q := domain.QueryRequest{
		MovieQuery: "Film", Location: domain.LocationConstraint{Query: "28202", RadiusMiles: 25},
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
