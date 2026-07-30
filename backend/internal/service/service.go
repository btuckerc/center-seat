package service

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"sync"
	"time"

	"centerseat/backend/internal/domain"
	"centerseat/backend/internal/providers"
	"centerseat/backend/internal/ranking"
)

type Service struct {
	discovery providers.Discovery
	inventory providers.Inventory
	fanout    int
}

func New(discovery providers.Discovery, inventory providers.Inventory, fanout int) *Service {
	if fanout < 1 {
		fanout = 6
	}
	return &Service{discovery: discovery, inventory: inventory, fanout: fanout}
}

func (s *Service) ProviderStatuses() []domain.ProviderStatus {
	if reporter, ok := s.discovery.(providers.StatusReporter); ok {
		discovery := reporter.ProviderStatus("discovery")
		if inventoryReporter, ok := s.inventory.(providers.StatusReporter); ok {
			return []domain.ProviderStatus{discovery, inventoryReporter.ProviderStatus("inventory")}
		}
		return []domain.ProviderStatus{discovery}
	}
	return []domain.ProviderStatus{
		{Name: s.discovery.Name(), Kind: "discovery", Status: "degraded", Configured: true, Message: "Provider does not report health"},
		{Name: s.inventory.Name(), Kind: "inventory", Status: "degraded", Configured: true, Message: "Provider does not report health"},
	}
}

func (s *Service) MovieSuggestions(ctx context.Context, query string, limit int) ([]domain.MovieSuggestion, error) {
	suggester, ok := s.discovery.(providers.MovieSuggester)
	if !ok {
		return []domain.MovieSuggestion{}, nil
	}
	query = strings.TrimSpace(query)
	if len(query) < 2 || len(query) > 160 {
		return nil, errors.New("movie suggestion query must be between 2 and 160 characters")
	}
	if limit < 1 || limit > 10 {
		limit = 6
	}
	requestCtx, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	return suggester.SuggestMovies(requestCtx, query, limit)
}

func (s *Service) RefreshRecommendation(ctx context.Context, source domain.Recommendation, q domain.QueryRequest) (domain.Recommendation, error) {
	q.SetDefaults()
	inventory, err := s.inventory.GetAvailability(ctx, source.Showtime, true)
	if err != nil {
		return domain.Recommendation{}, err
	}
	showtime, ok := applyInventoryPricing(source.Showtime, inventory, q)
	if !ok {
		return domain.Recommendation{}, errors.New("live ticket price exceeded the query constraint or was unavailable")
	}
	recommendation, ok := ranking.BestBlock(showtime, inventory, q)
	if !ok {
		return domain.Recommendation{}, errors.New("the selected screening no longer has an eligible seat block")
	}
	recommendation.Rank = source.Rank
	recommendation.Score = combinedScore(recommendation.Score, showtime, q)
	return recommendation, nil
}

func (s *Service) Query(ctx context.Context, queryID string, q domain.QueryRequest) (domain.QueryResponse, error) {
	started := time.Now()
	q.SetDefaults()
	if err := q.Validate(); err != nil {
		return domain.QueryResponse{}, err
	}
	discoveryCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
	showtimes, err := s.discovery.Discover(discoveryCtx, q)
	cancel()
	if err != nil {
		return domain.QueryResponse{}, fmt.Errorf("discover showtimes: %w", err)
	}
	filtered := filterAndPrune(showtimes, q)
	coverage := domain.Coverage{ScreeningsDiscovered: len(showtimes), ScreeningsPruned: len(filtered)}
	if len(filtered) == 0 {
		now := time.Now().UTC()
		coverage.ElapsedMS = int(time.Since(started).Milliseconds())
		return domain.QueryResponse{QueryID: queryID, Status: "no_match", GeneratedAt: now, ExpiresAt: now.Add(30 * time.Second), Coverage: coverage, Alternatives: []domain.Recommendation{}, Warnings: []string{"No screenings matched every hard constraint"}}, nil
	}

	type result struct {
		recommendation domain.Recommendation
		err            error
	}
	results := make(chan result, len(filtered))
	sem := make(chan struct{}, s.fanout)
	var wg sync.WaitGroup
	for _, showtime := range filtered {
		if !s.inventory.Supports(showtime) {
			continue
		}
		wg.Add(1)
		go func(st domain.Showtime) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			inventoryCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
			defer cancel()
			inventory, err := s.inventory.GetAvailability(inventoryCtx, st, false)
			if err != nil {
				results <- result{err: err}
				return
			}
			st, ok := applyInventoryPricing(st, inventory, q)
			if !ok {
				results <- result{err: errors.New("live ticket price exceeded the query constraint or was unavailable")}
				return
			}
			recommendation, ok := ranking.BestBlock(st, inventory, q)
			if !ok {
				results <- result{err: errors.New("no eligible contiguous block")}
				return
			}
			recommendation.Score = combinedScore(recommendation.Score, st, q)
			results <- result{recommendation: recommendation}
		}(showtime)
	}
	wg.Wait()
	close(results)

	recommendations := make([]domain.Recommendation, 0, len(filtered))
	degraded := 0
	for candidate := range results {
		coverage.InventoriesChecked++
		if candidate.err != nil {
			degraded++
			continue
		}
		coverage.InventoriesFresh++
		recommendations = append(recommendations, candidate.recommendation)
	}
	coverage.ProvidersDegraded = degraded
	sort.Slice(recommendations, func(i, j int) bool { return recommendations[i].Score > recommendations[j].Score })
	status := "complete"
	warnings := []string{}
	if degraded > 0 {
		status = "partial"
		warnings = append(warnings, fmt.Sprintf("%d inventory checks did not complete", degraded))
	}
	if len(recommendations) == 0 {
		status = "no_match"
		warnings = append(warnings, "Matching screenings had no eligible contiguous seat blocks")
	}

	// Re-read only the winning inventory to reduce stale-seat risk without holding a seat.
	if len(recommendations) > 0 {
		verifyCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
		fresh, err := s.inventory.GetAvailability(verifyCtx, recommendations[0].Showtime, true)
		cancel()
		if err == nil {
			if verified, ok := ranking.BestBlock(recommendations[0].Showtime, fresh, q); ok {
				verified.Score = combinedScore(verified.Score, verified.Showtime, q)
				recommendations[0] = verified
			}
		} else {
			status = "partial"
			warnings = append(warnings, "The winning screening could not be refreshed a final time")
		}
	}

	now := time.Now().UTC()
	coverage.ElapsedMS = int(time.Since(started).Milliseconds())
	response := domain.QueryResponse{QueryID: queryID, Status: status, GeneratedAt: now, ExpiresAt: now.Add(8 * time.Second), Coverage: coverage, Alternatives: []domain.Recommendation{}, Warnings: warnings}
	if len(recommendations) > 0 {
		for i := range recommendations {
			recommendations[i].Rank = i + 1
		}
		response.Winner = &recommendations[0]
		limit := len(recommendations)
		if limit > 5 {
			limit = 5
		}
		if limit > 1 {
			response.Alternatives = append([]domain.Recommendation(nil), recommendations[1:limit]...)
			for i := range response.Alternatives {
				response.Alternatives[i].SeatMap = nil
			}
		}
	}
	return response, nil
}

func (s *Service) Showtimes(ctx context.Context, queryID string, q domain.QueryRequest) (domain.ShowtimeQueryResponse, error) {
	started := time.Now()
	q.SetDefaults()
	if err := q.Validate(); err != nil {
		return domain.ShowtimeQueryResponse{}, err
	}
	discoveryCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
	showtimes, err := s.discovery.Discover(discoveryCtx, q)
	cancel()
	if err != nil {
		return domain.ShowtimeQueryResponse{}, fmt.Errorf("discover showtimes: %w", err)
	}
	filtered := filterShowtimes(showtimes, q, false)
	now := time.Now().UTC()
	status := "complete"
	warnings := []string{"Showtime-only queries do not claim per-seat availability; use the seat-query endpoint when live inventory is connected"}
	if len(filtered) == 0 {
		status = "no_match"
		warnings = append(warnings, "No screenings matched every discovery constraint")
	}
	return domain.ShowtimeQueryResponse{
		QueryID: queryID, Status: status, GeneratedAt: now, ExpiresAt: now.Add(5 * time.Minute),
		Coverage: domain.Coverage{
			ScreeningsDiscovered: len(showtimes), ScreeningsPruned: len(filtered),
			ElapsedMS: int(time.Since(started).Milliseconds()),
		},
		Showtimes: filtered, Warnings: warnings,
	}, nil
}

func filterAndPrune(showtimes []domain.Showtime, q domain.QueryRequest) []domain.Showtime {
	return filterShowtimes(showtimes, q, true)
}

func filterShowtimes(showtimes []domain.Showtime, q domain.QueryRequest, requireReservedSeating bool) []domain.Showtime {
	formats := toSet(q.Formats)
	amenities := toSet(q.AmenitiesRequired)
	result := make([]domain.Showtime, 0, len(showtimes))
	for _, st := range showtimes {
		if (requireReservedSeating && !st.ReservedSeating) || st.DistanceMiles > q.MaxDistanceMiles {
			continue
		}
		if q.MinStartNoticeMinutes > 0 && st.StartsAt.Before(time.Now().Add(time.Duration(q.MinStartNoticeMinutes)*time.Minute)) {
			continue
		}
		if len(formats) > 0 && !formats[strings.ToLower(st.Format)] {
			continue
		}
		if q.MaxTotalPrice != nil && st.TotalPrice != nil && *st.TotalPrice > *q.MaxTotalPrice {
			continue
		}
		if q.MaxTotalPrice != nil && st.TotalPrice == nil && !q.AllowUnknownPrice && !requireReservedSeating {
			continue
		}
		if q.AudioDescription && !st.AudioDescription {
			continue
		}
		if q.Captions != "any" && q.Captions != "" && st.Captions != q.Captions {
			continue
		}
		if !hasAll(st.Amenities, amenities) || !matchesTime(st.StartsAt, q.Time) {
			continue
		}
		result = append(result, st)
	}
	sort.Slice(result, func(i, j int) bool {
		return preliminaryScore(result[i], q) > preliminaryScore(result[j], q)
	})
	if len(result) > q.CandidateLimit {
		result = result[:q.CandidateLimit]
	}
	return result
}

func matchesTime(t time.Time, constraint domain.TimeConstraint) bool {
	if constraint.Mode == "" || constraint.Mode == "any" {
		return true
	}
	if constraint.Timezone != "" {
		if location, err := time.LoadLocation(constraint.Timezone); err == nil {
			t = t.In(location)
		}
	}
	clock := t.Format("15:04")
	switch constraint.Mode {
	case "inside":
		return clock >= constraint.Start && clock <= constraint.End
	case "outside":
		return clock < constraint.Start || clock > constraint.End
	case "before":
		return clock <= constraint.End
	case "after":
		return clock >= constraint.Start
	default:
		return false
	}
}

func applyInventoryPricing(showtime domain.Showtime, inventory domain.Inventory, query domain.QueryRequest) (domain.Showtime, bool) {
	if showtime.TotalPrice == nil && inventory.TicketPrice != nil {
		fee := 0.0
		if inventory.TicketFee != nil {
			fee = *inventory.TicketFee
		}
		total := math.Round((*inventory.TicketPrice+fee)*float64(query.TicketCount)*100) / 100
		showtime.TotalPrice = &total
		showtime.Currency = inventory.Currency
	}
	if query.MaxTotalPrice == nil {
		return showtime, true
	}
	if showtime.TotalPrice == nil {
		return showtime, query.AllowUnknownPrice
	}
	return showtime, *showtime.TotalPrice <= *query.MaxTotalPrice
}

func preliminaryScore(st domain.Showtime, q domain.QueryRequest) float64 {
	score := 100 - st.DistanceMiles*1.8
	if st.Format == "dolby" || st.Format == "imax" {
		score += 5
	}
	if contains(st.Amenities, "recliner") {
		score += 3
	}
	return score
}

func combinedScore(seatScore float64, st domain.Showtime, q domain.QueryRequest) float64 {
	distancePenalty := 0.0
	if q.MaxDistanceMiles > 0 {
		distancePenalty = (st.DistanceMiles / q.MaxDistanceMiles) * 7
	}
	formatBonus := 0.0
	if st.Format == "dolby" || st.Format == "imax" {
		formatBonus = 1.5
	}
	result := seatScore - distancePenalty + formatBonus
	if result > 100 {
		result = 100
	}
	if result < 0 {
		result = 0
	}
	return float64(int(result*100+0.5)) / 100
}

func toSet(values []string) map[string]bool {
	result := map[string]bool{}
	for _, value := range values {
		result[strings.ToLower(value)] = true
	}
	return result
}

func hasAll(values []string, required map[string]bool) bool {
	if len(required) == 0 {
		return true
	}
	actual := toSet(values)
	for item := range required {
		if !actual[item] {
			return false
		}
	}
	return true
}

func contains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
