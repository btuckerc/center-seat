package providers

import (
	"context"
	"fmt"
	"hash/fnv"
	"strings"
	"time"

	"centerseat/backend/internal/domain"
)

type Demo struct{}

func (Demo) Name() string { return "deterministic-demo" }

func (Demo) Discover(ctx context.Context, q domain.QueryRequest) ([]domain.Showtime, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}
	date, _ := time.Parse(time.DateOnly, q.Dates.Start)
	loc := time.Local
	if q.Time.Timezone != "" {
		if parsed, err := time.LoadLocation(q.Time.Timezone); err == nil {
			loc = parsed
		}
	}
	date = time.Date(date.Year(), date.Month(), date.Day(), 0, 0, 0, 0, loc)
	price := func(v float64) *float64 { return &v }
	title := strings.TrimSpace(q.MovieQuery)
	rows := []struct {
		venue, room, format, clock string
		distance, price            float64
		amenities                  []string
		captions                   string
		audio                      bool
	}{
		{"Crown Arc Cinema", "Auditorium 6", "dolby", "18:20", 4.2, 21.49, []string{"recliner", "laser_projection", "reserved_seating"}, "closed", true},
		{"Parkside 12", "IMAX 1", "imax", "19:10", 8.1, 24.00, []string{"laser_projection", "reserved_seating"}, "none", true},
		{"Rialto House", "Screen 2", "standard", "20:35", 2.7, 15.25, []string{"recliner", "alcohol", "reserved_seating"}, "open", false},
		{"Northline Cinema", "Dolby 3", "dolby", "21:45", 11.6, 19.75, []string{"recliner", "dine_in", "laser_projection", "reserved_seating"}, "closed", true},
		{"Crown Arc Cinema", "Auditorium 4", "standard", "16:05", 4.2, 14.50, []string{"recliner", "reserved_seating"}, "none", false},
		{"Museum Film Center", "Hall A", "standard", "14:30", 6.8, 12.00, []string{"reserved_seating"}, "open", true},
		{"Eastgate Screens", "XD 1", "xd", "22:30", 13.4, 18.95, []string{"recliner", "reserved_seating"}, "closed", false},
		{"Garden Cinema", "Screen 5", "3d", "12:40", 17.9, 17.25, []string{"reserved_seating"}, "none", false},
	}
	result := make([]domain.Showtime, 0, len(rows))
	for i, row := range rows {
		var hour, minute int
		_, _ = fmt.Sscanf(row.clock, "%d:%d", &hour, &minute)
		result = append(result, domain.Showtime{
			ID: fmt.Sprintf("demo-%02d", i+1), MovieTitle: title,
			VenueName: row.venue, AuditoriumName: row.room,
			StartsAt: time.Date(date.Year(), date.Month(), date.Day(), hour, minute, 0, 0, loc),
			Format:   row.format, DistanceMiles: row.distance, TotalPrice: price(row.price * float64(q.TicketCount)),
			Currency: "USD", Amenities: row.amenities, Captions: row.captions,
			AudioDescription: row.audio, ReservedSeating: true,
			BookingURL: "https://example.com/book/" + fmt.Sprintf("demo-%02d", i+1), InventoryProvider: "deterministic-demo",
		})
	}
	return result, nil
}

func (Demo) Supports(s domain.Showtime) bool { return s.InventoryProvider == "deterministic-demo" }

func (Demo) GetAvailability(ctx context.Context, showtime domain.Showtime, final bool) (domain.Inventory, error) {
	select {
	case <-ctx.Done():
		return domain.Inventory{}, ctx.Err()
	default:
	}
	h := fnv.New32a()
	_, _ = h.Write([]byte(showtime.ID))
	seed := int(h.Sum32())
	seats := make([]domain.Seat, 0, 126)
	for row := 0; row < 9; row++ {
		for col := 0; col < 14; col++ {
			status := "available"
			fingerprint := (seed + row*17 + col*11) % 13
			if fingerprint == 0 || fingerprint == 4 || (row == 4 && col >= 5 && col <= 7 && showtime.ID == "demo-02") {
				status = "sold"
			}
			if fingerprint == 9 {
				status = "held"
			}
			seatType := "recliner"
			if row == 8 && (col == 1 || col == 12) {
				seatType = "wheelchair"
			}
			if row == 8 && (col == 2 || col == 11) {
				seatType = "companion"
			}
			seats = append(seats, domain.Seat{
				ID:    fmt.Sprintf("%s-%c-%d", showtime.ID, 'A'+row, col+1),
				Label: fmt.Sprintf("%c%d", 'A'+row, col+1), Row: string(rune('A' + row)), Index: col,
				X: float64(col) / 13, Y: float64(row) / 8, Type: seatType, Status: status,
			})
		}
	}
	return domain.Inventory{ShowtimeID: showtime.ID, Seats: seats, Confidence: "exact_coordinates", ObservedAt: time.Now().UTC(), FreshFor: 8 * time.Second}, nil
}
