package ranking

import (
	"fmt"
	"math"
	"testing"
	"time"

	"centerseat/backend/internal/domain"
)

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
