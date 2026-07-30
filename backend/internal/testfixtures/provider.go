package testfixtures

import (
	"context"
	"fmt"
	"time"

	"centerseat/backend/internal/domain"
)

// Provider is deterministic test input. Production configuration cannot select it.
type Provider struct{}

func (Provider) Name() string { return "test-fixture" }

func (Provider) ProviderStatus(kind string) domain.ProviderStatus {
	now := time.Now().UTC()
	return domain.ProviderStatus{
		Name: "test-fixture", Kind: kind, Status: "healthy", Configured: true,
		Message: "Automated-test fixture", LastSuccessAt: &now,
	}
}

func (Provider) Discover(ctx context.Context, q domain.QueryRequest) ([]domain.Showtime, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	day, err := time.Parse(time.DateOnly, q.Dates.Start)
	if err != nil {
		return nil, err
	}
	location := time.Local
	if q.Time.Timezone != "" {
		if parsed, loadErr := time.LoadLocation(q.Time.Timezone); loadErr == nil {
			location = parsed
		}
	}
	clocks := [][2]int{{18, 20}, {20, 35}, {14, 30}, {22, 30}}
	result := make([]domain.Showtime, 0, len(clocks))
	for index, clock := range clocks {
		result = append(result, domain.Showtime{
			ID: fmt.Sprintf("fixture-%d", index), MovieTitle: q.MovieQuery,
			VenueName: "Test Theater", AuditoriumName: "Test Auditorium",
			StartsAt:      time.Date(day.Year(), day.Month(), day.Day(), clock[0], clock[1], 0, 0, location),
			Format:        []string{"dolby", "standard", "standard", "xd"}[index],
			DistanceMiles: float64(index + 1), Amenities: []string{"recliner", "reserved_seating"},
			Captions: "closed", AudioDescription: true, ReservedSeating: true,
			InventoryProvider: "test-fixture",
		})
	}
	return result, nil
}

func (Provider) Supports(showtime domain.Showtime) bool {
	return showtime.InventoryProvider == "test-fixture"
}

func (Provider) GetAvailability(ctx context.Context, showtime domain.Showtime, _ bool) (domain.Inventory, error) {
	if err := ctx.Err(); err != nil {
		return domain.Inventory{}, err
	}
	seats := make([]domain.Seat, 0, 9*14)
	for row := 0; row < 9; row++ {
		for column := 0; column < 14; column++ {
			label := fmt.Sprintf("%c%d", 'A'+row, column+1)
			status := "available"
			if (row*14+column)%17 == 0 {
				status = "sold"
			}
			seats = append(seats, domain.Seat{
				ID: showtime.ID + "-" + label, Label: label, Row: fmt.Sprintf("%c", 'A'+row), Index: column,
				X: float64(column) / 13, Y: float64(row) / 8, Type: "recliner", Status: status,
			})
		}
	}
	return domain.Inventory{
		ShowtimeID: showtime.ID, Seats: seats, Confidence: "exact_coordinates",
		ObservedAt: time.Now().UTC(), FreshFor: 8 * time.Second,
	}, nil
}
