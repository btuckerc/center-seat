package ranking

import (
	"testing"
	"time"

	"centerseat/backend/internal/domain"
)

func TestBestBlockUsesGeometryAndContiguity(t *testing.T) {
	showtime := domain.Showtime{ID: "s1", BookingURL: "https://example.com"}
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
