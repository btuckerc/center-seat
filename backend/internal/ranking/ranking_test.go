package ranking

import (
	"fmt"
	"math"
	"strings"
	"testing"
	"time"

	"centerseat/backend/internal/domain"
)

func TestBestBlockIncludesExactPriceEstimateAndUnknownFees(t *testing.T) {
	price, fee := 15.0, 2.5
	inventory := domain.Inventory{
		Confidence: "exact_coordinates", TicketPrice: &price, TicketFee: &fee, Currency: "USD",
		ObservedAt: time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC),
		Seats: []domain.Seat{
			{ID: "a1", Label: "E1", Row: "E", Index: 0, X: .46, Y: .60, Type: "recliner", Status: "available"},
			{ID: "a2", Label: "E2", Row: "E", Index: 1, X: .54, Y: .60, Type: "recliner", Status: "available"},
		},
	}
	got, ok := BestBlock(domain.Showtime{ID: "s1"}, inventory, domain.QueryRequest{TicketCount: 2, SeatProfile: "balanced"})
	if !ok {
		t.Fatal("expected a recommendation")
	}
	if got.Price.TicketCount != 2 || got.Price.Currency == nil || *got.Price.Currency != "USD" ||
		got.Price.TicketPrice == nil || *got.Price.TicketPrice != 15 || got.Price.FeePerTicket == nil ||
		*got.Price.FeePerTicket != 2.5 || got.Price.EstimatedTotal == nil || *got.Price.EstimatedTotal != 35 ||
		!got.Price.FeesIncluded || !got.Price.IsEstimate {
		t.Fatalf("incorrect estimate: %#v", got.Price)
	}
	inventory.TicketFee = nil
	got, ok = BestBlock(domain.Showtime{ID: "s1"}, inventory, domain.QueryRequest{TicketCount: 2, SeatProfile: "balanced"})
	if !ok || got.Price.FeePerTicket != nil || got.Price.FeesIncluded || got.Price.EstimatedTotal == nil || *got.Price.EstimatedTotal != 30 {
		t.Fatalf("unknown provider fee was treated as zero or omitted from the estimate: %#v", got.Price)
	}
}

func TestBestBlockUsesGeometryAndContiguity(t *testing.T) {
	showtime := domain.Showtime{ID: "s1"}
	inventory := domain.Inventory{Confidence: "exact_coordinates", ObservedAt: time.Now(), Seats: []domain.Seat{
		{ID: "a1", Label: "E1", Row: "E", Index: 0, X: .05, Y: .60, Type: "recliner", Status: "available"},
		{ID: "a2", Label: "E2", Row: "E", Index: 1, X: .12, Y: .60, Type: "recliner", Status: "available"},
		{ID: "a7", Label: "E7", Row: "E", Index: 6, X: .46, Y: .60, Type: "recliner", Status: "available"},
		{ID: "a8", Label: "E8", Row: "E", Index: 7, X: .54, Y: .60, Type: "recliner", Status: "available"},
	}}
	q := domain.QueryRequest{TicketCount: 2, SeatProfile: "balanced"}
	got, ok := BestBlock(showtime, inventory, q)
	if !ok {
		t.Fatal("expected a recommendation")
	}
	if got.Seats[0].Label != "E7" || got.Seats[1].Label != "E8" {
		t.Fatalf("expected centered contiguous block, got %v", got.Seats)
	}
}

func TestBestBlockExcludesUnavailableAndAccessibilitySeatsByDefault(t *testing.T) {
	showtime := domain.Showtime{ID: "s1"}
	inventory := domain.Inventory{Confidence: "exact_coordinates", Seats: []domain.Seat{
		{ID: "sold", Label: "F6", Row: "F", Index: 5, X: .46, Y: .65, Type: "recliner", Status: "sold"},
		{ID: "wheelchair", Label: "F7", Row: "F", Index: 6, X: .54, Y: .65, Type: "wheelchair", Status: "available"},
		{ID: "available", Label: "G1", Row: "G", Index: 0, X: .10, Y: .75, Type: "recliner", Status: "available"},
	}}
	q := domain.QueryRequest{TicketCount: 1, SeatProfile: "balanced"}
	got, ok := BestBlock(showtime, inventory, q)
	if !ok {
		t.Fatal("expected fallback seat")
	}
	if got.Seats[0].ID != "available" {
		t.Fatalf("unexpected seat %s", got.Seats[0].ID)
	}
}

func TestDeadCenterNeverSelectsAnOffCenterSeat(t *testing.T) {
	seats := make([]domain.Seat, 0, 9*14)
	for row := 0; row < 9; row++ {
		for index := 0; index < 14; index++ {
			label := fmt.Sprintf("%c%d", 'A'+row, index+1)
			seats = append(seats, domain.Seat{
				ID: label, Label: label, Row: fmt.Sprintf("%c", 'A'+row), Index: index,
				X: float64(index) / 13, Y: float64(row) / 8, Type: "recliner", Status: "available",
			})
		}
	}
	recommendation, ok := BestBlock(domain.Showtime{ID: "s1"}, domain.Inventory{
		Seats: seats, Confidence: "exact_coordinates", ObservedAt: time.Now(),
	}, domain.QueryRequest{TicketCount: 1, SeatProfile: "dead_center"})
	if !ok {
		t.Fatal("expected recommendation")
	}
	seat := recommendation.Seats[0]
	if seat.Label != "E7" && seat.Label != "E8" {
		t.Fatalf("expected E7 or E8, got %s", seat.Label)
	}
	if math.Abs(seat.X-.5) > .04 {
		t.Fatalf("seat is not geometrically centered: x=%f", seat.X)
	}
	if recommendation.SeatMap == nil || recommendation.SeatMap.Target.X != .5 || recommendation.SeatMap.Target.Y != .5 {
		t.Fatal("missing dead-center target metadata")
	}
}

func TestDeadCenterReturnsAvailableChoicesInsideFourSeatCenterZone(t *testing.T) {
	inventory := domain.Inventory{Confidence: "exact_coordinates", ObservedAt: time.Now(), Seats: []domain.Seat{
		{ID: "C5", Label: "C5", Row: "C", Index: 5, X: .28, Y: .50, Type: "recliner", Status: "available"},
		{ID: "C6", Label: "C6", Row: "C", Index: 6, X: .42, Y: .50, Type: "recliner", Status: "available"},
		{ID: "C7", Label: "C7", Row: "C", Index: 7, X: .48, Y: .50, Type: "recliner", Status: "available"},
		{ID: "C8", Label: "C8", Row: "C", Index: 8, X: .52, Y: .50, Type: "recliner", Status: "sold"},
		{ID: "C9", Label: "C9", Row: "C", Index: 9, X: .58, Y: .50, Type: "recliner", Status: "available"},
		{ID: "C10", Label: "C10", Row: "C", Index: 10, X: .72, Y: .50, Type: "recliner", Status: "available"},
	}}
	recommendation, ok := BestBlock(domain.Showtime{ID: "s1"}, inventory, domain.QueryRequest{
		TicketCount: 1, SeatProfile: "dead_center",
	})
	if !ok {
		t.Fatal("expected recommendation")
	}
	if len(recommendation.SeatOptions) != 3 {
		t.Fatalf("expected three available center choices, got %v", recommendation.SeatOptions)
	}
	if recommendation.SeatMap == nil || len(recommendation.SeatMap.RecommendedZone) != 4 {
		t.Fatalf("expected four-seat geometric zone, got %#v", recommendation.SeatMap)
	}
	if recommendation.SeatMap.RecommendedZone[0] != "C7" || recommendation.SeatMap.RecommendedZone[1] != "C8" {
		t.Fatalf("zone is not ordered from the center: %v", recommendation.SeatMap.RecommendedZone)
	}
}

func TestDeadCenterFallsBackWhenEntireCenterZoneIsUnavailable(t *testing.T) {
	inventory := domain.Inventory{Confidence: "exact_coordinates", ObservedAt: time.Now(), Seats: []domain.Seat{
		{ID: "C4", Label: "C4", Row: "C", Index: 4, X: .20, Y: .50, Type: "recliner", Status: "available"},
		{ID: "C5", Label: "C5", Row: "C", Index: 5, X: .35, Y: .50, Type: "recliner", Status: "sold"},
		{ID: "C6", Label: "C6", Row: "C", Index: 6, X: .45, Y: .50, Type: "recliner", Status: "sold"},
		{ID: "C7", Label: "C7", Row: "C", Index: 7, X: .55, Y: .50, Type: "recliner", Status: "held"},
		{ID: "C8", Label: "C8", Row: "C", Index: 8, X: .65, Y: .50, Type: "recliner", Status: "sold"},
		{ID: "C9", Label: "C9", Row: "C", Index: 9, X: .80, Y: .50, Type: "recliner", Status: "available"},
	}}
	recommendation, ok := BestBlock(domain.Showtime{ID: "s1"}, inventory, domain.QueryRequest{
		TicketCount: 1, SeatProfile: "dead_center",
	})
	if !ok {
		t.Fatal("expected closest available fallback")
	}
	if len(recommendation.SeatOptions) != 2 || recommendation.SeatOptions[0].ID != recommendation.Seats[0].ID {
		t.Fatalf("expected both equally strong fallbacks with the winner first, got %#v", recommendation.SeatOptions)
	}
	if len(recommendation.SeatMap.RecommendedZone) != 4 {
		t.Fatalf("expected the unavailable four-seat ideal zone to remain visible, got %v", recommendation.SeatMap.RecommendedZone)
	}
	if got := recommendation.Explanation[0]; got != "C4 and C9 are the closest available fallbacks; all 4 geometric center positions are unavailable" {
		t.Fatalf("unexpected explanation: %q", got)
	}
	if recommendation.ProfileMatch != "closest_fallback" {
		t.Fatalf("expected an explicit fallback classification, got %q", recommendation.ProfileMatch)
	}
}

func TestTwoThirdsBackStaysInPreferredDepthWhenEligibleSeatsExist(t *testing.T) {
	inventory := domain.Inventory{Confidence: "exact_coordinates", ObservedAt: time.Now(), Seats: []domain.Seat{
		{ID: "D16", Label: "D16", Row: "D", Index: 15, X: .485, Y: .267, Type: "recliner", Status: "available"},
		{ID: "G8", Label: "G8", Row: "G", Index: 7, X: .50, Y: .534, Type: "recliner", Status: "available"},
		{ID: "G9", Label: "G9", Row: "G", Index: 8, X: .55, Y: .534, Type: "recliner", Status: "available"},
		{ID: "H16", Label: "H16", Row: "H", Index: 15, X: .50, Y: .630, Type: "recliner", Status: "sold"},
		{ID: "I5", Label: "I5", Row: "I", Index: 4, X: .49, Y: .720, Type: "recliner", Status: "available"},
	}}
	recommendation, ok := BestBlock(domain.Showtime{ID: "odyssey"}, inventory, domain.QueryRequest{
		TicketCount: 1, SeatProfile: "two_thirds_back",
	})
	if !ok {
		t.Fatal("expected a recommendation")
	}
	if recommendation.Seats[0].ID == "D16" || recommendation.Seats[0].Y < .50 || recommendation.Seats[0].Y > .78 {
		t.Fatalf("expected a seat in the 2/3-back depth zone, got %#v", recommendation.Seats[0])
	}
	if recommendation.ProfileMatch != "preferred_zone" {
		t.Fatalf("expected preferred-zone classification, got %q", recommendation.ProfileMatch)
	}
	if recommendation.SeatMap == nil || recommendation.SeatMap.PreferredDepth == nil || recommendation.SeatMap.PreferredDepth.Minimum != .50 || recommendation.SeatMap.PreferredDepth.Maximum != .78 {
		t.Fatalf("missing preferred-depth metadata: %#v", recommendation.SeatMap)
	}
	if len(recommendation.SeatOptions) < 2 {
		t.Fatalf("expected multiple strong available choices, got %#v", recommendation.SeatOptions)
	}
}

func TestTwoThirdsBackLabelsFallbackWhenPreferredDepthIsSoldOut(t *testing.T) {
	inventory := domain.Inventory{Confidence: "exact_coordinates", ObservedAt: time.Now(), Seats: []domain.Seat{
		{ID: "D16", Label: "D16", Row: "D", Index: 15, X: .485, Y: .267, Type: "recliner", Status: "available"},
		{ID: "H16", Label: "H16", Row: "H", Index: 15, X: .50, Y: .630, Type: "recliner", Status: "sold"},
	}}
	recommendation, ok := BestBlock(domain.Showtime{ID: "odyssey"}, inventory, domain.QueryRequest{
		TicketCount: 1, SeatProfile: "two_thirds_back",
	})
	if !ok {
		t.Fatal("expected the closest available fallback")
	}
	if recommendation.Seats[0].ID != "D16" || recommendation.ProfileMatch != "closest_fallback" {
		t.Fatalf("expected D16 as an explicit fallback, got %#v", recommendation)
	}
	if !strings.Contains(recommendation.Explanation[0], "no eligible seat remained") {
		t.Fatalf("fallback was not explained: %q", recommendation.Explanation[0])
	}
}

func TestCustomZoneMapsNormalizedAreaOntoAuditoriumGeometry(t *testing.T) {
	zone := domain.SeatZone{MinimumX: .62, MaximumX: .95, MinimumY: .62, MaximumY: .92}
	inventory := domain.Inventory{Confidence: "exact_coordinates", ObservedAt: time.Now(), Seats: []domain.Seat{
		{ID: "center", Label: "E8", Row: "E", Index: 7, X: .50, Y: .50, Type: "recliner", Status: "available"},
		{ID: "custom", Label: "H13", Row: "H", Index: 12, X: .78, Y: .78, Type: "recliner", Status: "available"},
		{ID: "edge", Label: "J15", Row: "J", Index: 14, X: .94, Y: .94, Type: "recliner", Status: "available"},
	}}
	recommendation, ok := BestBlock(domain.Showtime{ID: "custom-zone"}, inventory, domain.QueryRequest{
		TicketCount: 1, SeatProfile: "custom", CustomSeatZone: &zone,
	})
	if !ok {
		t.Fatal("expected a custom-zone recommendation")
	}
	if recommendation.Seats[0].ID != "custom" || recommendation.ProfileMatch != "preferred_zone" {
		t.Fatalf("expected the seat inside the drawn zone, got %#v", recommendation)
	}
	if recommendation.SeatMap == nil || recommendation.SeatMap.PreferredZone == nil {
		t.Fatal("expected custom zone metadata on the seat map")
	}
	if math.Abs(recommendation.SeatMap.Target.X-.785) > 1e-9 || math.Abs(recommendation.SeatMap.Target.Y-.77) > 1e-9 {
		t.Fatalf("custom target did not use the selected zone center: %#v", recommendation.SeatMap.Target)
	}
}

func TestCustomZoneLabelsNearestFallbackWhenSelectedAreaIsUnavailable(t *testing.T) {
	zone := domain.SeatZone{MinimumX: .4, MaximumX: .6, MinimumY: .6, MaximumY: .8}
	inventory := domain.Inventory{Confidence: "exact_coordinates", ObservedAt: time.Now(), Seats: []domain.Seat{
		{ID: "sold", Label: "H8", Row: "H", Index: 7, X: .50, Y: .70, Type: "recliner", Status: "sold"},
		{ID: "fallback", Label: "G8", Row: "G", Index: 7, X: .50, Y: .52, Type: "recliner", Status: "available"},
	}}
	recommendation, ok := BestBlock(domain.Showtime{ID: "custom-fallback"}, inventory, domain.QueryRequest{
		TicketCount: 1, SeatProfile: "custom", CustomSeatZone: &zone,
	})
	if !ok {
		t.Fatal("expected a nearest fallback")
	}
	if recommendation.Seats[0].ID != "fallback" || recommendation.ProfileMatch != "closest_fallback" {
		t.Fatalf("expected an explicit fallback outside the unavailable zone, got %#v", recommendation)
	}
}
