package domain

import (
	"testing"
	"time"
)

func TestQueryAllowsProviderSupportedCoarseLocation(t *testing.T) {
	today := time.Now().Format(time.DateOnly)
	query := QueryRequest{
		MovieQuery: "Test Film",
		Location:   LocationConstraint{Query: "28202", RadiusMiles: 25},
		Dates:      DateConstraint{Start: today, End: today},
		Time:       TimeConstraint{Mode: "any", Timezone: "America/New_York"},
	}
	query.SetDefaults()
	if err := query.Validate(); err != nil {
		t.Fatalf("coarse provider location should pass generic validation: %v", err)
	}
}

func TestQueryStillRequiresSomeLocationSignal(t *testing.T) {
	query := QueryRequest{
		MovieQuery:  "Test Film",
		Location:    LocationConstraint{RadiusMiles: 25},
		Time:        TimeConstraint{Mode: "any", Timezone: "America/New_York"},
		TicketCount: 1, SeatProfile: "dead_center", MaxDistanceMiles: 25, CandidateLimit: 6,
	}
	query.SetDefaults()
	if err := query.Validate(); err == nil {
		t.Fatal("expected a missing-location validation error")
	}
}

func TestQueryUsesTheFullAutomaticCandidateSafetyCeiling(t *testing.T) {
	query := QueryRequest{Dates: DateConstraint{Start: "2026-08-01", End: "2026-08-07"}}
	query.SetDefaults()
	if query.CandidateLimit != 64 {
		t.Fatalf("expected the automatic safety ceiling for a dense short range, got %d", query.CandidateLimit)
	}

	query = QueryRequest{Dates: DateConstraint{Start: "2026-08-01", End: "2026-08-31"}}
	query.SetDefaults()
	if query.CandidateLimit != 64 {
		t.Fatalf("expected the automatic safety ceiling, got %d", query.CandidateLimit)
	}
}

func TestCustomSeatProfileRequiresValidNormalizedZone(t *testing.T) {
	base := QueryRequest{
		MovieQuery: "Test Film", Location: LocationConstraint{Query: "28202", RadiusMiles: 25},
		Dates: DateConstraint{Start: "2026-08-01", End: "2026-08-01"}, TicketCount: 1, SeatProfile: "custom", MaxDistanceMiles: 25,
		Time: TimeConstraint{Mode: "any", Timezone: "America/New_York"},
	}
	base.SetDefaults()
	if err := base.Validate(); err == nil {
		t.Fatal("expected custom profile without a zone to fail")
	}

	invalid := base
	invalid.CustomSeatZone = &SeatZone{MinimumX: .8, MaximumX: .2, MinimumY: .4, MaximumY: .7}
	if err := invalid.Validate(); err == nil {
		t.Fatal("expected inverted custom zone to fail")
	}

	valid := base
	valid.CustomSeatZone = &SeatZone{MinimumX: .2, MaximumX: .8, MinimumY: .4, MaximumY: .75}
	if err := valid.Validate(); err != nil {
		t.Fatalf("expected normalized custom zone to pass: %v", err)
	}
}
func TestQueryRequiresTimezoneAndValidClockTimes(t *testing.T) {
	query := QueryRequest{
		MovieQuery: "Film", Location: LocationConstraint{Query: "28202", RadiusMiles: 25},
		Dates:       DateConstraint{Start: "2026-08-01", End: "2026-08-01"},
		TicketCount: 1, SeatProfile: "balanced", MaxDistanceMiles: 25, CandidateLimit: 10,
		Time: TimeConstraint{Mode: "any"},
	}
	query.SetDefaults()
	for _, test := range []struct {
		name string
		set  func(*QueryRequest)
	}{
		{"missing timezone", func(q *QueryRequest) { q.Time.Timezone = "" }},
		{"invalid timezone", func(q *QueryRequest) { q.Time.Timezone = "Not/AZone" }},
		{"non-padded time", func(q *QueryRequest) { q.Time.Start = "7pm" }},
		{"out-of-range time", func(q *QueryRequest) { q.Time.End = "25:00" }},
	} {
		t.Run(test.name, func(t *testing.T) {
			candidate := query
			test.set(&candidate)
			if err := candidate.Validate(); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}
