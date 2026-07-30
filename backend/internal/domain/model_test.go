package domain

import (
	"testing"
	"time"
)

func TestQueryAllowsProviderSupportedCoarseLocation(t *testing.T) {
	today := time.Now().Format(time.DateOnly)
	query := QueryRequest{
		MovieQuery:  "Test Film",
		Location:    LocationConstraint{Query: "28202", RadiusMiles: 25},
		Dates:       DateConstraint{Start: today, End: today},
		TicketCount: 1, SeatProfile: "dead_center", MaxDistanceMiles: 25, CandidateLimit: 6,
	}
	query.SetDefaults()
	if err := query.Validate(); err != nil {
		t.Fatalf("coarse provider location should pass generic validation: %v", err)
	}
}

func TestQueryStillRequiresSomeLocationSignal(t *testing.T) {
	today := time.Now().Format(time.DateOnly)
	query := QueryRequest{
		MovieQuery:  "Test Film",
		Location:    LocationConstraint{RadiusMiles: 25},
		Dates:       DateConstraint{Start: today, End: today},
		TicketCount: 1, SeatProfile: "dead_center", MaxDistanceMiles: 25, CandidateLimit: 6,
	}
	query.SetDefaults()
	if err := query.Validate(); err == nil {
		t.Fatal("expected a missing-location validation error")
	}
}
