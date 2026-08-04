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

type profilePolicy struct {
	target            Targets
	horizontalWeight  float64
	depthWeight       float64
	aisleWeight       float64
	orphanWeight      float64
	confidenceWeight  float64
	preferredDepth    domain.DepthRange
	hasPreferredDepth bool
	preferredZone     *domain.SeatZone
}

type rankedBlock struct {
	seats       []domain.Seat
	cost        float64
	parts       map[string]float64
	midX        float64
	midY        float64
	inPreferred bool
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

func policyForQuery(q domain.QueryRequest) profilePolicy {
	profile := q.SeatProfile
	policy := profilePolicy{
		target: profileTargets(profile), horizontalWeight: .48, depthWeight: .32,
		aisleWeight: .08, orphanWeight: .06, confidenceWeight: .06,
	}
	switch profile {
	case "dead_center":
		policy.horizontalWeight = .44
		policy.depthWeight = .44
		policy.aisleWeight = .04
		policy.orphanWeight = .04
		policy.confidenceWeight = .04
		policy.preferredDepth = domain.DepthRange{Minimum: .38, Maximum: .62}
		policy.hasPreferredDepth = true
	case "two_thirds_back":
		policy.horizontalWeight = .40
		policy.depthWeight = .48
		policy.aisleWeight = .04
		policy.orphanWeight = .04
		policy.confidenceWeight = .04
		policy.preferredDepth = domain.DepthRange{Minimum: .50, Maximum: .78}
		policy.hasPreferredDepth = true
	case "custom":
		if q.CustomSeatZone != nil {
			zone := *q.CustomSeatZone
			policy.preferredZone = &zone
			policy.target = Targets{X: (zone.MinimumX + zone.MaximumX) / 2, Y: (zone.MinimumY + zone.MaximumY) / 2}
			policy.horizontalWeight = .45
			policy.depthWeight = .45
			policy.aisleWeight = .02
			policy.orphanWeight = .04
			policy.confidenceWeight = .04
		}
	}
	return policy
}

func (policy profilePolicy) hasPreferredArea() bool {
	return policy.hasPreferredDepth || policy.preferredZone != nil
}

func (policy profilePolicy) blockInPreferredArea(block []domain.Seat) bool {
	if len(block) == 0 {
		return false
	}
	if policy.hasPreferredDepth && (block[0].Y < policy.preferredDepth.Minimum || block[0].Y > policy.preferredDepth.Maximum) {
		return false
	}
	if policy.preferredZone == nil {
		return true
	}
	minimumX, maximumX := block[0].X, block[0].X
	for _, seat := range block[1:] {
		minimumX = math.Min(minimumX, seat.X)
		maximumX = math.Max(maximumX, seat.X)
	}
	zone := policy.preferredZone
	return minimumX >= zone.MinimumX && maximumX <= zone.MaximumX && block[0].Y >= zone.MinimumY && block[0].Y <= zone.MaximumY
}

func (policy profilePolicy) seatInPreferredArea(seat domain.Seat) bool {
	return policy.blockInPreferredArea([]domain.Seat{seat})
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
	policy := policyForQuery(q)
	target := policy.target
	candidates := make([]rankedBlock, 0)

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
			cost := policy.horizontalWeight*horizontal + policy.depthWeight*depth + policy.aisleWeight*aisle + policy.orphanWeight*orphan + policy.confidenceWeight*confidence
			inPreferred := policy.blockInPreferredArea(block)
			candidates = append(candidates, rankedBlock{
				seats: append([]domain.Seat(nil), block...), cost: cost, midX: midX, midY: midY, inPreferred: inPreferred,
				parts: map[string]float64{
					"horizontal_alignment": round2(100 * (1 - horizontal)),
					"viewing_depth":        round2(100 * (1 - depth)),
					"party_contiguity":     100,
					"geometry_confidence":  round2(100 * (1 - confidence)),
				},
			})
		}
	}
	if len(candidates) == 0 {
		return domain.Recommendation{}, false
	}
	pool := candidates
	fallback := false
	if policy.hasPreferredArea() {
		preferred := make([]rankedBlock, 0, len(candidates))
		for _, candidate := range candidates {
			if candidate.inPreferred {
				preferred = append(preferred, candidate)
			}
		}
		if len(preferred) > 0 {
			pool = preferred
		} else {
			fallback = true
		}
	}
	sort.SliceStable(pool, func(i, j int) bool {
		if math.Abs(pool[i].cost-pool[j].cost) > 1e-9 {
			return pool[i].cost < pool[j].cost
		}
		if math.Abs(pool[i].midY-target.Y) != math.Abs(pool[j].midY-target.Y) {
			return math.Abs(pool[i].midY-target.Y) < math.Abs(pool[j].midY-target.Y)
		}
		if math.Abs(pool[i].midX-target.X) != math.Abs(pool[j].midX-target.X) {
			return math.Abs(pool[i].midX-target.X) < math.Abs(pool[j].midX-target.X)
		}
		return pool[i].seats[0].ID < pool[j].seats[0].ID
	})
	bestCandidate := pool[0]
	best := bestCandidate.seats
	bestCandidate.parts["preferred_zone"] = 100
	if fallback {
		bestCandidate.parts["preferred_zone"] = 0
	}
	score := math.Max(0, math.Min(100, 100*(1-bestCandidate.cost)))
	labels := best[0].Label
	if len(best) > 1 {
		labels = best[0].Label + "–" + best[len(best)-1].Label
	}
	lead := fmt.Sprintf("%s is the strongest contiguous block for the %s profile", labels, humanProfile(q.SeatProfile))
	profileMatch := "preferred_zone"
	if policy.hasPreferredArea() && fallback {
		lead = fmt.Sprintf("%s is the closest available fallback; no eligible seat remained in the preferred %s zone", labels, humanProfile(q.SeatProfile))
		profileMatch = "closest_fallback"
	} else if policy.hasPreferredArea() {
		lead = fmt.Sprintf("%s is the strongest available choice in the preferred %s zone", labels, humanProfile(q.SeatProfile))
	}
	explanation := []string{
		lead,
		fmt.Sprintf("Block center is x %.1f%% against a %.1f%% horizontal target", ((best[0].X+best[len(best)-1].X)/2)*100, target.X*100),
		fmt.Sprintf("Row %s is %.0f%% of the way from the screen", best[0].Row, best[0].Y*100),
		"Live availability was refreshed before this recommendation was returned",
	}
	seatOptions, recommendedZone := profileSeatChoices(inventory.Seats, q, policy, pool, excludedRows)
	if q.TicketCount == 1 && q.SeatProfile == "dead_center" && len(recommendedZone) > 1 {
		idealOptions := availableZoneSeats(inventory.Seats, recommendedZone, q)
		unavailable := len(recommendedZone) - len(idealOptions)
		if len(idealOptions) == 0 {
			profileMatch = "closest_fallback"
			bestCandidate.parts["preferred_zone"] = 0
			verb, noun := "is", "fallback"
			if len(seatOptions) > 1 {
				verb, noun = "are", "fallbacks"
			}
			explanation[0] = fmt.Sprintf("%s %s the closest available %s; all %d geometric center positions are unavailable", joinSeatLabels(seatOptions), verb, noun, len(recommendedZone))
		} else if len(idealOptions) == 1 {
			seatOptions = idealOptions
			explanation[0] = fmt.Sprintf("%s is the best available choice in the %d-seat geometric center zone", joinSeatLabels(seatOptions), len(recommendedZone))
		} else {
			seatOptions = idealOptions
			explanation[0] = fmt.Sprintf("%s are the best available choices in the %d-seat geometric center zone", joinSeatLabels(seatOptions), len(recommendedZone))
		}
		if unavailable > 0 {
			explanation = append(explanation[:1], append([]string{fmt.Sprintf("%d ideal-zone %s currently unavailable", unavailable, pluralizeSeat(unavailable))}, explanation[1:]...)...)
		}
	}
	var preferredDepth *domain.DepthRange
	if policy.hasPreferredDepth {
		depth := policy.preferredDepth
		preferredDepth = &depth
	}
	var preferredZone *domain.SeatZone
	if policy.preferredZone != nil {
		zone := *policy.preferredZone
		preferredZone = &zone
	}
	return domain.Recommendation{
		Showtime: showtime, Seats: best, SeatOptions: seatOptions, Score: round2(score), Confidence: inventory.Confidence,
		Explanation: explanation, ScoreBreakdown: bestCandidate.parts, ProfileMatch: profileMatch, VerifiedAt: inventory.ObservedAt, BookingURL: showtime.BookingURL,
		SeatMap: &domain.SeatMap{
			Seats: inventory.Seats, Target: domain.GeometryPoint{X: target.X, Y: target.Y}, PreferredDepth: preferredDepth, PreferredZone: preferredZone, RecommendedZone: recommendedZone,
			Confidence: inventory.Confidence, ObservedAt: inventory.ObservedAt,
		},
	}, true
}

func availableZoneSeats(seats []domain.Seat, zone []string, q domain.QueryRequest) []domain.Seat {
	byID := make(map[string]domain.Seat, len(seats))
	for _, seat := range seats {
		byID[seat.ID] = seat
	}
	available := make([]domain.Seat, 0, len(zone))
	for _, id := range zone {
		if seat, ok := byID[id]; ok && eligible(seat, q) {
			available = append(available, seat)
		}
	}
	return available
}

func profileSeatChoices(seats []domain.Seat, q domain.QueryRequest, policy profilePolicy, ranked []rankedBlock, excludedRows map[string]bool) ([]domain.Seat, []string) {
	if len(ranked) == 0 {
		return nil, nil
	}
	best := ranked[0].seats
	if q.TicketCount != 1 {
		ids := make([]string, 0, len(best))
		for _, seat := range best {
			ids = append(ids, seat.ID)
		}
		return append([]domain.Seat(nil), best...), ids
	}
	physical := make([]domain.Seat, 0)
	for _, seat := range seats {
		if excludedRows[seat.Row] || !eligibleType(seat, q) {
			continue
		}
		if policy.hasPreferredArea() && !policy.seatInPreferredArea(seat) {
			continue
		}
		physical = append(physical, seat)
	}
	if len(physical) == 0 {
		for _, seat := range seats {
			if !excludedRows[seat.Row] && eligibleType(seat, q) {
				physical = append(physical, seat)
			}
		}
	}
	sort.SliceStable(physical, func(i, j int) bool {
		left := policy.horizontalWeight*math.Abs(physical[i].X-policy.target.X) + policy.depthWeight*math.Abs(physical[i].Y-policy.target.Y)
		right := policy.horizontalWeight*math.Abs(physical[j].X-policy.target.X) + policy.depthWeight*math.Abs(physical[j].Y-policy.target.Y)
		if math.Abs(left-right) > 1e-9 {
			return left < right
		}
		return physical[i].ID < physical[j].ID
	})
	if len(physical) > 4 {
		physical = physical[:4]
	}
	zone := make([]string, 0, len(physical))
	for _, seat := range physical {
		zone = append(zone, seat.ID)
	}
	options := make([]domain.Seat, 0, 4)
	seen := map[string]bool{}
	for _, candidate := range ranked {
		seat := candidate.seats[0]
		if candidate.cost-ranked[0].cost > .06 || seen[seat.ID] {
			continue
		}
		seen[seat.ID] = true
		options = append(options, seat)
		if len(options) == 4 {
			break
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
		"aisle": "aisle-friendly", "front": "closer-to-screen", "back": "back-row", "custom": "custom",
	}[profile]
}

func round2(v float64) float64 { return math.Round(v*100) / 100 }
