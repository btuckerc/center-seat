package ranking

import (
	"fmt"
	"math"
	"sort"
	"strings"

	"centerseat/backend/internal/domain"
)

type Targets struct {
	X, Y  float64
	Aisle bool
}

func profileTargets(profile string) Targets {
	switch profile {
	case "dead_center":
		return Targets{X: .50, Y: .50}
	case "two_thirds_back":
		return Targets{X: .50, Y: .66}
	case "aisle":
		return Targets{X: .50, Y: .60, Aisle: true}
	case "front":
		return Targets{X: .50, Y: .30}
	case "back":
		return Targets{X: .50, Y: .85}
	default:
		return Targets{X: .50, Y: .60}
	}
}

func confidencePenalty(confidence string) float64 {
	switch confidence {
	case "exact_coordinates":
		return 0
	case "rendered_geometry":
		return .02
	case "row_geometry":
		return .06
	case "label_heuristic":
		return .13
	default:
		return .22
	}
}

func eligible(seat domain.Seat, q domain.QueryRequest) bool {
	if seat.Status != "available" {
		return false
	}
	return eligibleType(seat, q)
}

func eligibleType(seat domain.Seat, q domain.QueryRequest) bool {
	if seat.Type == "wheelchair" {
		return q.WheelchairSpaces > 0
	}
	if seat.Type == "companion" {
		return q.CompanionSeats > 0
	}
	return seat.Type == "standard" || seat.Type == "recliner" || seat.Type == "sofa"
}

func BestBlock(showtime domain.Showtime, inventory domain.Inventory, q domain.QueryRequest) (domain.Recommendation, bool) {
	if !meetsMinimumConfidence(inventory.Confidence, q.MinimumGeometryConfidence) {
		return domain.Recommendation{}, false
	}
	excludedRows := firstRows(inventory.Seats, q.ExcludeFirstRows)
	rows := map[string][]domain.Seat{}
	for _, seat := range inventory.Seats {
		if !excludedRows[seat.Row] && eligible(seat, q) {
			rows[seat.Row] = append(rows[seat.Row], seat)
		}
	}
	target := profileTargets(q.SeatProfile)
	bestCost := math.MaxFloat64
	var best []domain.Seat
	bestParts := map[string]float64{}

	for _, rowSeats := range rows {
		sort.Slice(rowSeats, func(i, j int) bool { return rowSeats[i].Index < rowSeats[j].Index })
		for start := 0; start+q.TicketCount <= len(rowSeats); start++ {
			block := rowSeats[start : start+q.TicketCount]
			contiguous := true
			for i := 1; i < len(block); i++ {
				if block[i].Index != block[i-1].Index+1 || block[i].X-block[i-1].X > .14 {
					contiguous = false
					break
				}
			}
			if !contiguous {
				continue
			}
			midX := (block[0].X + block[len(block)-1].X) / 2
			midY := block[0].Y
			horizontal := math.Abs(midX - target.X)
			depth := math.Abs(midY - target.Y)
			rowEdge := math.Min(block[0].X, 1-block[len(block)-1].X)
			aisle := 0.0
			if target.Aisle {
				aisle = rowEdge
			} else if rowEdge < .04 {
				aisle = .05
			}
			orphan := 0.0
			if start == 1 || len(rowSeats)-(start+len(block)) == 1 {
				orphan = .08
			}
			confidence := confidencePenalty(inventory.Confidence)
			cost := .48*horizontal + .32*depth + .08*aisle + .06*orphan + .06*confidence
			if cost < bestCost {
				bestCost = cost
				best = append([]domain.Seat(nil), block...)
				bestParts = map[string]float64{
					"horizontal_alignment": round2(100 * (1 - horizontal)),
					"viewing_depth":        round2(100 * (1 - depth)),
					"party_contiguity":     100,
					"geometry_confidence":  round2(100 * (1 - confidence)),
				}
			}
		}
	}
	if len(best) == 0 {
		return domain.Recommendation{}, false
	}
	score := math.Max(0, math.Min(100, 100*(1-bestCost)))
	labels := best[0].Label
	if len(best) > 1 {
		labels = best[0].Label + "–" + best[len(best)-1].Label
	}
	explanation := []string{
		fmt.Sprintf("%s is the strongest contiguous block for the %s profile", labels, humanProfile(q.SeatProfile)),
		fmt.Sprintf("Block center is x %.1f%% against a %.1f%% horizontal target", ((best[0].X+best[len(best)-1].X)/2)*100, target.X*100),
		fmt.Sprintf("Row %s is %.0f%% of the way from the screen", best[0].Row, best[0].Y*100),
		"Live availability was refreshed before this recommendation was returned",
	}
	seatOptions, recommendedZone := centerSeatOptions(inventory.Seats, q, target, best, excludedRows)
	if q.TicketCount == 1 && q.SeatProfile == "dead_center" && len(recommendedZone) > 1 {
		unavailable := len(recommendedZone) - len(seatOptions)
		if len(seatOptions) == 0 {
			seatOptions = append([]domain.Seat(nil), best...)
			explanation[0] = fmt.Sprintf("%s is the closest available seat; all %d geometric center-zone positions are unavailable", joinSeatLabels(seatOptions), len(recommendedZone))
		} else if len(seatOptions) == 1 {
			explanation[0] = fmt.Sprintf("%s is the best available choice in the %d-seat geometric center zone", joinSeatLabels(seatOptions), len(recommendedZone))
		} else {
			explanation[0] = fmt.Sprintf("%s are the best available choices in the %d-seat geometric center zone", joinSeatLabels(seatOptions), len(recommendedZone))
		}
		if unavailable > 0 {
			explanation = append(explanation[:1], append([]string{fmt.Sprintf("%d ideal-zone %s currently unavailable", unavailable, pluralizeSeat(unavailable))}, explanation[1:]...)...)
		}
	}
	return domain.Recommendation{
		Showtime: showtime, Seats: best, SeatOptions: seatOptions, Score: round2(score), Confidence: inventory.Confidence,
		Explanation: explanation, ScoreBreakdown: bestParts, VerifiedAt: inventory.ObservedAt, BookingURL: showtime.BookingURL,
		SeatMap: &domain.SeatMap{
			Seats: inventory.Seats, Target: domain.GeometryPoint{X: target.X, Y: target.Y}, RecommendedZone: recommendedZone,
			Confidence: inventory.Confidence, ObservedAt: inventory.ObservedAt,
		},
	}, true
}

func centerSeatOptions(seats []domain.Seat, q domain.QueryRequest, target Targets, best []domain.Seat, excludedRows map[string]bool) ([]domain.Seat, []string) {
	if len(best) == 0 {
		return nil, nil
	}
	if q.TicketCount != 1 || q.SeatProfile != "dead_center" {
		ids := make([]string, 0, len(best))
		for _, seat := range best {
			ids = append(ids, seat.ID)
		}
		return append([]domain.Seat(nil), best...), ids
	}
	candidates := make([]domain.Seat, 0)
	for _, seat := range seats {
		if seat.Row == best[0].Row && !excludedRows[seat.Row] && eligibleType(seat, q) {
			candidates = append(candidates, seat)
		}
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		left := math.Abs(candidates[i].X - target.X)
		right := math.Abs(candidates[j].X - target.X)
		if left == right {
			return candidates[i].Index < candidates[j].Index
		}
		return left < right
	})
	if len(candidates) > 4 {
		candidates = candidates[:4]
	}
	zone := make([]string, 0, len(candidates))
	options := make([]domain.Seat, 0, len(candidates))
	for _, seat := range candidates {
		zone = append(zone, seat.ID)
		if seat.Status == "available" {
			options = append(options, seat)
		}
	}
	return options, zone
}

func joinSeatLabels(seats []domain.Seat) string {
	if len(seats) == 0 {
		return "No seats"
	}
	labels := make([]string, 0, len(seats))
	for _, seat := range seats {
		labels = append(labels, seat.Label)
	}
	if len(labels) == 1 {
		return labels[0]
	}
	if len(labels) == 2 {
		return labels[0] + " and " + labels[1]
	}
	return fmt.Sprintf("%s, and %s", strings.Join(labels[:len(labels)-1], ", "), labels[len(labels)-1])
}

func pluralizeSeat(count int) string {
	if count == 1 {
		return "seat is"
	}
	return "seats are"
}

func meetsMinimumConfidence(actual, minimum string) bool {
	if minimum == "" {
		return true
	}
	grade := map[string]int{
		"label_heuristic":   1,
		"row_geometry":      2,
		"rendered_geometry": 3,
		"exact_coordinates": 4,
	}
	return grade[actual] >= grade[minimum]
}

func firstRows(seats []domain.Seat, count int) map[string]bool {
	excluded := map[string]bool{}
	if count <= 0 {
		return excluded
	}
	type rowPosition struct {
		label string
		y     float64
	}
	positions := map[string]float64{}
	for _, seat := range seats {
		if existing, ok := positions[seat.Row]; !ok || seat.Y < existing {
			positions[seat.Row] = seat.Y
		}
	}
	rows := make([]rowPosition, 0, len(positions))
	for label, y := range positions {
		rows = append(rows, rowPosition{label: label, y: y})
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].y == rows[j].y {
			return rows[i].label < rows[j].label
		}
		return rows[i].y < rows[j].y
	})
	if count > len(rows) {
		count = len(rows)
	}
	for _, row := range rows[:count] {
		excluded[row.label] = true
	}
	return excluded
}

func humanProfile(profile string) string {
	return map[string]string{
		"balanced": "best overall", "dead_center": "dead-center", "two_thirds_back": "two-thirds-back",
		"aisle": "aisle-friendly", "front": "closer-to-screen", "back": "back-row",
	}[profile]
}

func round2(v float64) float64 { return math.Round(v*100) / 100 }
