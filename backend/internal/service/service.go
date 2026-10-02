package service

import (
	"context"
	"errors"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strings"
	"time"

	"centerseat/backend/internal/domain"
	"centerseat/backend/internal/geocode"
	"centerseat/backend/internal/providers"
	"centerseat/backend/internal/ranking"
)

var (
	ErrScreeningStarted  = errors.New("screening started or is inside the minimum start notice")
	ErrSeatsUnavailable  = errors.New("the selected screening no longer has an eligible seat block")
	ErrPriceExceeded     = errors.New("live ticket price exceeded the query constraint or was unavailable")
	ErrLocationNotFound  = errors.New("location not found; use a ZIP code or coordinates")
	postalCodeInLocation = regexp.MustCompile(`\b[0-9]{5}\b`)
)

type Service struct {
	discovery providers.Discovery
	inventory providers.Inventory
	resolver  geocode.Resolver
	fanout    int
	// now decides which screenings have started and stamps result expiry; tests pin it.
	now func() time.Time
}

type inventoryCandidateResult struct {
	index          int
	recommendation domain.Recommendation
	outcome        string
	failureReason  string
	dateKey        string
}

// alternativesEnvelope is the winner plus four mapless alternatives.
const alternativesEnvelope = 5

func New(discovery providers.Discovery, inventory providers.Inventory, fanout int, resolver geocode.Resolver) *Service {
	if fanout < 1 {
		fanout = 6
	}
	if policy, ok := inventory.(providers.InventoryReadPolicy); ok {
		if providerLimit := policy.MaxConcurrentInventoryReads(); providerLimit > 0 && providerLimit < fanout {
			fanout = providerLimit
		}
	}
	return &Service{discovery: discovery, inventory: inventory, resolver: resolver, fanout: fanout, now: time.Now}
}

func (s *Service) ProviderStatuses() []domain.ProviderStatus {
	if reporter, ok := s.discovery.(providers.StatusReporter); ok {
		discovery := reporter.ProviderStatus("discovery")
		if s.resolver != nil {
			discovery.LocationMode = "text_or_coordinates"
		}
		if inventoryReporter, ok := s.inventory.(providers.StatusReporter); ok {
			return []domain.ProviderStatus{discovery, inventoryReporter.ProviderStatus("inventory")}
		}
		return []domain.ProviderStatus{discovery}
	}
	discovery := domain.ProviderStatus{Name: s.discovery.Name(), Kind: "discovery", Status: "degraded", Configured: true, Message: "Provider does not report health"}
	inventory := domain.ProviderStatus{Name: s.inventory.Name(), Kind: "inventory", Status: "degraded", Configured: true, Message: "Provider does not report health"}
	if s.resolver != nil {
		discovery.LocationMode = "text_or_coordinates"
	}
	return []domain.ProviderStatus{discovery, inventory}
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

// PrefetchDiscovery runs the discovery half of a query the user is still composing so a caching
// provider has the showtime lists ready when the search is submitted. It never reads seat maps.
// Providers without a discovery cache are skipped.
func (s *Service) PrefetchDiscovery(ctx context.Context, q domain.QueryRequest) error {
	prefetcher, ok := s.discovery.(providers.DiscoveryPrefetcher)
	if !ok {
		return nil
	}
	q.SetDefaults()
	if err := q.Validate(); err != nil {
		return err
	}
	if _, err := s.resolveLocation(ctx, &q); err != nil {
		return err
	}
	discoveryCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	return prefetcher.PrefetchDiscovery(discoveryCtx, q)
}

func (s *Service) RefreshRecommendation(ctx context.Context, source domain.Recommendation, q domain.QueryRequest) (domain.Recommendation, error) {
	q.SetDefaults()
	if !source.Showtime.StartsAt.After(earliestStart(s.now(), q)) {
		return domain.Recommendation{}, ErrScreeningStarted
	}
	inventory, err := s.inventory.GetAvailability(ctx, source.Showtime, true)
	if err != nil {
		return domain.Recommendation{}, fmt.Errorf("read live inventory: %w", err)
	}
	showtime, ok := applyInventoryPricing(source.Showtime, inventory, q)
	if !ok {
		return domain.Recommendation{}, ErrPriceExceeded
	}
	recommendation, ok := ranking.BestBlock(showtime, inventory, q)
	if !ok {
		return domain.Recommendation{}, ErrSeatsUnavailable
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
	resolved, err := s.resolveLocation(ctx, &q)
	if err != nil {
		return domain.QueryResponse{}, err
	}
	discoveryStarted := time.Now()
	discoveryCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
	showtimes, err := s.discovery.Discover(discoveryCtx, q)
	cancel()
	if err != nil {
		return domain.QueryResponse{}, fmt.Errorf("discover showtimes: %w", err)
	}
	eligibleShowtimes := filterShowtimes(showtimes, q, true, s.now())
	coverage := domain.Coverage{
		DatesRequested: dateRangeDays(q.Dates), DatesWithScreenings: distinctShowtimeDates(eligibleShowtimes, q),
		ScreeningsDiscovered: len(showtimes), ScreeningsPruned: len(eligibleShowtimes),
		InventoryFailureReasons: map[string]int{}, DiscoveryMS: int(time.Since(discoveryStarted).Milliseconds()),
	}
	if len(eligibleShowtimes) == 0 {
		now := s.now().UTC()
		coverage.ElapsedMS = int(time.Since(started).Milliseconds())
		return domain.QueryResponse{QueryID: queryID, Status: "no_match", GeneratedAt: now, ExpiresAt: now.Add(30 * time.Second), ResolvedLocation: resolved, Coverage: coverage, Alternatives: []domain.Recommendation{}, Warnings: []string{"No screenings matched every hard constraint"}}, nil
	}
	candidates := eligibleShowtimes
	if len(candidates) > q.CandidateLimit {
		candidates = candidates[:q.CandidateLimit]
	}

	recommendations := make([]domain.Recommendation, 0, len(candidates))
	dateTargets := minimumDateCoverage(eligibleShowtimes, q, 2)
	successfulByDate := map[string]int{}
	failedUpperBounds := make([]domain.Showtime, 0)
	processed := make([]bool, len(eligibleShowtimes))
	inventoryStarted := time.Now()
	candidateDates := make([]string, len(candidates))
	for index, showtime := range candidates {
		candidateDates[index] = showtimeDateKey(showtime.StartsAt, q)
	}
	// scheduled marks reads started and not abandoned. A conclusive stop abandons in-flight reads
	// and unmarks them, so final verification can resume the pipeline if it lowers the leader.
	scheduled := make([]bool, len(candidates))
	// runPipeline keeps every provider slot busy with screenings that can still matter: one that
	// could outrank the leader, one its date needs for coverage, or any while fewer than five
	// results exist to fill the winner and alternatives. The leader only rises while reads
	// complete, so a skipped screening stays ruled out until verification lowers the leader.
	runPipeline := func() {
		runCtx, cancelRun := context.WithCancel(ctx)
		defer cancelRun()
		results := make(chan inventoryCandidateResult, len(candidates))
		inFlightByDate := map[string]int{}
		inFlight := 0
		fill := func() {
			for index := range candidates {
				if inFlight >= s.fanout {
					return
				}
				date := candidateDates[index]
				if scheduled[index] || len(recommendations) >= alternativesEnvelope && !screeningCanMatter(candidates[index], recommendations, successfulByDate[date]+inFlightByDate[date], dateTargets[date], q) {
					continue
				}
				scheduled[index] = true
				inFlight++
				inFlightByDate[date]++
				go func() { results <- s.evaluateInventoryCandidate(runCtx, index, candidates[index], q) }()
			}
		}
		fill()
		for inFlight > 0 {
			candidate := <-results
			inFlight--
			inFlightByDate[candidate.dateKey]--
			if ctx.Err() != nil {
				return
			}
			processed[candidate.index] = true
			coverage.InventoriesChecked++
			switch candidate.outcome {
			case "failed":
				coverage.InventoriesFailed++
				coverage.InventoryFailureReasons[candidate.failureReason]++
				failedUpperBounds = append(failedUpperBounds, candidates[candidate.index])
			case "not_available":
				successfulByDate[candidate.dateKey]++
				coverage.ScreeningsUnavailable++
			case "price_rejected":
				coverage.InventoriesFresh++
				successfulByDate[candidate.dateKey]++
				coverage.ScreeningsPriceRejected++
			case "unavailable":
				coverage.InventoriesFresh++
				successfulByDate[candidate.dateKey]++
				coverage.ScreeningsUnavailable++
			case "recommended":
				coverage.InventoriesFresh++
				successfulByDate[candidate.dateKey]++
				recommendations = append(recommendations, candidate.recommendation)
			}
			coverage.DatesCompared = len(successfulByDate)
			sortRecommendations(recommendations)
			remaining := unprocessedShowtimes(eligibleShowtimes, processed)
			if len(recommendations) >= alternativesEnvelope && adaptiveCoverageIsConclusive(coverage, recommendations, dateCoverageSatisfied(successfulByDate, dateTargets), remaining, failedUpperBounds, q) {
				for index := range candidates {
					if scheduled[index] && !processed[index] {
						scheduled[index] = false
					}
				}
				return
			}
			fill()
		}
	}
	runPipeline()
	if err := ctx.Err(); err != nil {
		return domain.QueryResponse{}, fmt.Errorf("seat query canceled: %w", err)
	}
	coverage.InventoryMS = int(time.Since(inventoryStarted).Milliseconds())
	coverage.ProvidersDegraded = coverage.InventoriesFailed
	status := "complete"
	warnings := []string{}
	if len(recommendations) == 0 {
		status = "no_match"
		if coverage.InventoriesFresh > 0 {
			warnings = append(warnings, "Live availability was checked, but no screening satisfied every seat, party, accessibility, and price constraint")
		} else {
			warnings = append(warnings, "No live seat availability was available to rank")
		}
	}

	// Re-read the winner without holding a seat. If it changed, discard it and
	// promote the next ranked candidate through the same final check.
	verificationStarted := time.Now()
	verifiedShowtimes := map[string]bool{}
	for len(recommendations) > 0 {
		sortRecommendations(recommendations)
		winnerID := recommendations[0].Showtime.ID
		if verifiedShowtimes[winnerID] {
			coverage.WinnerVerified = true
			break
		}
		verifyCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		fresh, err := s.inventory.GetAvailability(verifyCtx, recommendations[0].Showtime, true)
		cancel()
		if err != nil {
			status = "partial"
			warnings = append(warnings, fmt.Sprintf("The winning screening could not be refreshed a final time (%s); its initial live map is shown", humanFailureReason(inventoryFailureReason(err))))
			break
		}
		showtime, priceOK := applyInventoryPricing(recommendations[0].Showtime, fresh, q)
		if !priceOK {
			warnings = append(warnings, fmt.Sprintf("%s changed during final verification, so the next ranked screening was promoted", recommendations[0].Showtime.VenueName))
			recommendations = recommendations[1:]
			runPipeline()
			continue
		}
		verified, seatsOK := ranking.BestBlock(showtime, fresh, q)
		if !seatsOK {
			warnings = append(warnings, fmt.Sprintf("%s changed during final verification, so the next ranked screening was promoted", recommendations[0].Showtime.VenueName))
			recommendations = recommendations[1:]
			runPipeline()
			continue
		}
		verified.Score = combinedScore(verified.Score, verified.Showtime, q)
		recommendations[0] = verified
		verifiedShowtimes[winnerID] = true
		sortRecommendations(recommendations)
		runPipeline()
		sortRecommendations(recommendations)
		if recommendations[0].Showtime.ID == winnerID {
			coverage.WinnerVerified = true
			break
		}
		warnings = append(warnings, fmt.Sprintf("%s changed rank during final verification, so the new leader was checked too", verified.Showtime.VenueName))
	}
	if err := ctx.Err(); err != nil {
		return domain.QueryResponse{}, fmt.Errorf("seat query canceled: %w", err)
	}
	if len(recommendations) == 0 {
		status = "no_match"
		warnings = append(warnings, "No screening remained eligible after final live verification")
	}
	coverage.VerificationMS = int(time.Since(verificationStarted).Milliseconds())
	remaining := unprocessedShowtimes(eligibleShowtimes, processed)
	coverage.RangeBestProven = coverage.WinnerVerified && adaptiveCoverageIsConclusive(coverage, recommendations, dateCoverageSatisfied(successfulByDate, dateTargets), remaining, failedUpperBounds, q)
	if !coverage.RangeBestProven && len(recommendations) > 0 {
		if status != "no_match" {
			status = "partial"
		}
		if coverage.InventoriesFailed > 0 {
			warnings = append(warnings, inventoryFailureWarning())
		} else if unresolved := unresolvedScreenings(remaining, recommendations, successfulByDate, dateTargets, q); unresolved > 0 {
			warnings = append(warnings, fmt.Sprintf("This is the best of %d live screenings compared, but %d additional matching screenings could not be ruled out within the automatic safety limit", coverage.InventoriesFresh, unresolved))
		}
	}

	now := s.now().UTC()
	coverage.ElapsedMS = int(time.Since(started).Milliseconds())
	response := domain.QueryResponse{QueryID: queryID, Status: status, GeneratedAt: now, ExpiresAt: now.Add(8 * time.Second), RefreshUntil: now.Add(15 * time.Minute), ResolvedLocation: resolved, Coverage: coverage, Alternatives: []domain.Recommendation{}, Warnings: warnings}
	if len(recommendations) > 0 {
		for i := range recommendations {
			recommendations[i].Rank = i + 1
		}
		response.Winner = &recommendations[0]
		limit := min(len(recommendations), alternativesEnvelope)
		if limit > 1 {
			response.Alternatives = append([]domain.Recommendation(nil), recommendations[1:limit]...)
			for i := range response.Alternatives {
				response.Alternatives[i].SeatMap = nil
			}
		}
	}
	return response, nil
}

func sortRecommendations(recommendations []domain.Recommendation) {
	sort.SliceStable(recommendations, func(i, j int) bool {
		if recommendations[i].Score != recommendations[j].Score {
			return recommendations[i].Score > recommendations[j].Score
		}
		if !recommendations[i].Showtime.StartsAt.Equal(recommendations[j].Showtime.StartsAt) {
			return recommendations[i].Showtime.StartsAt.Before(recommendations[j].Showtime.StartsAt)
		}
		return recommendations[i].Showtime.ID < recommendations[j].Showtime.ID
	})
}

func unprocessedShowtimes(showtimes []domain.Showtime, processed []bool) []domain.Showtime {
	remaining := make([]domain.Showtime, 0, len(showtimes))
	for index, showtime := range showtimes {
		if index >= len(processed) || !processed[index] {
			remaining = append(remaining, showtime)
		}
	}
	return remaining
}

func (s *Service) evaluateInventoryCandidate(ctx context.Context, index int, showtime domain.Showtime, q domain.QueryRequest) inventoryCandidateResult {
	date := showtimeDateKey(showtime.StartsAt, q)
	base := inventoryCandidateResult{index: index, dateKey: date}
	if !s.inventory.Supports(showtime) {
		base.outcome = "failed"
		base.failureReason = "provider_error"
		return base
	}
	inventoryCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	inventory, err := s.inventory.GetAvailability(inventoryCtx, showtime, false)
	if err != nil {
		reason := inventoryFailureReason(err)
		if reason == "not_available" {
			base.outcome = "not_available"
			return base
		}
		base.outcome = "failed"
		base.failureReason = reason
		return base
	}
	showtime, ok := applyInventoryPricing(showtime, inventory, q)
	if !ok {
		base.outcome = "price_rejected"
		return base
	}
	recommendation, ok := ranking.BestBlock(showtime, inventory, q)
	if !ok {
		base.outcome = "unavailable"
		return base
	}
	recommendation.Score = combinedScore(recommendation.Score, showtime, q)
	base.recommendation = recommendation
	base.outcome = "recommended"
	return base
}

func adaptiveCoverageIsConclusive(coverage domain.Coverage, recommendations []domain.Recommendation, dateCoverageComplete bool, unchecked []domain.Showtime, failedUpperBounds []domain.Showtime, q domain.QueryRequest) bool {
	if !dateCoverageComplete || coverage.InventoriesFresh == 0 || len(recommendations) == 0 {
		return false
	}
	for _, showtime := range unchecked {
		if candidateCouldOutrankLeader(showtime, recommendations, q) {
			return false
		}
	}
	for _, showtime := range failedUpperBounds {
		if candidateCouldOutrankLeader(showtime, recommendations, q) {
			return false
		}
	}
	return true
}

// candidateCouldOutrankLeader reports whether an unread or failed screening could still be ranked
// ahead of the leader by sortRecommendations: a higher score bound, or an equal bound that the
// start-time and ID tiebreaks would place first. Pruning and the range proof both use it.
func candidateCouldOutrankLeader(showtime domain.Showtime, recommendations []domain.Recommendation, q domain.QueryRequest) bool {
	if len(recommendations) == 0 {
		return true
	}
	leader := recommendations[0]
	upperBound := maximumPossibleScore(showtime, q)
	if upperBound != leader.Score {
		return upperBound > leader.Score
	}
	if !showtime.StartsAt.Equal(leader.Showtime.StartsAt) {
		return showtime.StartsAt.Before(leader.Showtime.StartsAt)
	}
	return showtime.ID < leader.Showtime.ID
}

// screeningCanMatter includes candidates that are still needed for date coverage.
func screeningCanMatter(showtime domain.Showtime, recommendations []domain.Recommendation, dateReads, dateTarget int, q domain.QueryRequest) bool {
	return candidateCouldOutrankLeader(showtime, recommendations, q) || dateReads < dateTarget
}

func unresolvedScreenings(unchecked []domain.Showtime, recommendations []domain.Recommendation, successfulByDate, dateTargets map[string]int, q domain.QueryRequest) int {
	count := 0
	for _, showtime := range unchecked {
		date := showtimeDateKey(showtime.StartsAt, q)
		if screeningCanMatter(showtime, recommendations, successfulByDate[date], dateTargets[date], q) {
			count++
		}
	}
	return count
}

func inventoryFailureReason(err error) string {
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return "timeout"
	}
	message := strings.ToLower(err.Error())
	switch {
	case strings.Contains(message, "http 408"):
		return "timeout"
	case strings.Contains(message, "http 429"):
		return "rate_limited"
	case strings.Contains(message, "http 401"), strings.Contains(message, "http 403"):
		return "provider_rejected"
	case strings.Contains(message, "http 404"), strings.Contains(message, "http 409"), strings.Contains(message, "http 410"), strings.Contains(message, "http 422"), strings.Contains(message, "no longer"):
		return "not_available"
	case strings.Contains(message, "read failed"), strings.Contains(message, "unexpected eof"), strings.Contains(message, "connection reset"), strings.Contains(message, "broken pipe"):
		return "transport_error"
	case strings.Contains(message, "decode"), strings.Contains(message, "payload"), strings.Contains(message, "seat geometry"):
		return "invalid_layout"
	default:
		return "provider_error"
	}
}

func inventoryFailureWarning() string {
	return "Some screenings changed before their live availability could be compared. CenterSeat automatically used the checks that completed successfully."
}

func humanFailureReason(reason string) string {
	switch reason {
	case "timeout":
		return "timed out"
	case "rate_limited":
		return "rate limited"
	case "provider_rejected":
		return "provider rejected"
	case "not_available":
		return "no longer available"
	case "invalid_layout":
		return "invalid layout"
	case "transport_error":
		return "connection interrupted"
	default:
		return "provider error"
	}
}

func (s *Service) Showtimes(ctx context.Context, queryID string, q domain.QueryRequest) (domain.ShowtimeQueryResponse, error) {
	started := time.Now()
	q.SetDefaults()
	if err := q.Validate(); err != nil {
		return domain.ShowtimeQueryResponse{}, err
	}
	resolved, err := s.resolveLocation(ctx, &q)
	if err != nil {
		return domain.ShowtimeQueryResponse{}, err
	}
	discoveryStarted := time.Now()
	discoveryCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
	showtimes, err := s.discovery.Discover(discoveryCtx, q)
	cancel()
	if err != nil {
		return domain.ShowtimeQueryResponse{}, fmt.Errorf("discover showtimes: %w", err)
	}
	filtered := filterShowtimes(showtimes, q, false, s.now())
	if len(filtered) > q.CandidateLimit {
		filtered = filtered[:q.CandidateLimit]
	}
	now := s.now().UTC()
	status := "complete"
	warnings := []string{"Showtime-only queries do not claim per-seat availability; use the seat-query endpoint when live inventory is connected"}
	if len(filtered) == 0 {
		status = "no_match"
		warnings = append(warnings, "No screenings matched every discovery constraint")
	}
	return domain.ShowtimeQueryResponse{
		QueryID: queryID, Status: status, GeneratedAt: now, ExpiresAt: now.Add(5 * time.Minute),
		ResolvedLocation: resolved,
		Coverage: domain.Coverage{
			DatesRequested:       dateRangeDays(q.Dates),
			DatesWithScreenings:  distinctShowtimeDates(filtered, q),
			ScreeningsDiscovered: len(showtimes), ScreeningsPruned: len(filtered),
			InventoryFailureReasons: map[string]int{},
			DiscoveryMS:             int(time.Since(discoveryStarted).Milliseconds()),
			ElapsedMS:               int(time.Since(started).Milliseconds()),
		},
		Showtimes: filtered, Warnings: warnings,
	}, nil
}

func (s *Service) resolveLocation(ctx context.Context, q *domain.QueryRequest) (*domain.ResolvedLocation, error) {
	if q.Location.Latitude != 0 || q.Location.Longitude != 0 || q.Location.Query == "" || s.resolver == nil {
		return nil, nil
	}
	place, err := s.resolver.Resolve(ctx, q.Location.Query)
	if err != nil {
		if postalCodeInLocation.MatchString(q.Location.Query) {
			return nil, nil
		}
		if errors.Is(err, geocode.ErrNotFound) {
			return nil, ErrLocationNotFound
		}
		return nil, fmt.Errorf("resolve location: %w", err)
	}
	q.Location.Latitude, q.Location.Longitude = place.Latitude, place.Longitude
	return &domain.ResolvedLocation{Label: place.Label, Latitude: place.Latitude, Longitude: place.Longitude}, nil
}

// earliestStart is the first start instant a screening may have: already-started screenings and
// those inside the minimum start notice are never recommended.
func earliestStart(now time.Time, q domain.QueryRequest) time.Time {
	return now.Add(time.Duration(q.MinStartNoticeMinutes) * time.Minute)
}

func filterShowtimes(showtimes []domain.Showtime, q domain.QueryRequest, requireReservedSeating bool, now time.Time) []domain.Showtime {
	formats := toSet(q.Formats)
	amenities := toSet(q.AmenitiesRequired)
	notBefore := earliestStart(now, q)
	result := make([]domain.Showtime, 0, len(showtimes))
	for _, st := range showtimes {
		if (requireReservedSeating && !st.ReservedSeating) || st.DistanceMiles > q.MaxDistanceMiles || !st.StartsAt.After(notBefore) {
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
	return orderShowtimesAcrossDates(result, q)
}

func orderShowtimesAcrossDates(showtimes []domain.Showtime, q domain.QueryRequest) []domain.Showtime {
	buckets := map[string][]domain.Showtime{}
	keys := make([]string, 0)
	for _, showtime := range showtimes {
		key := showtimeDateKey(showtime.StartsAt, q)
		if _, ok := buckets[key]; !ok {
			keys = append(keys, key)
		}
		buckets[key] = append(buckets[key], showtime)
	}
	sort.Strings(keys)
	for _, key := range keys {
		bucket := buckets[key]
		sort.SliceStable(bucket, func(i, j int) bool {
			left, right := preliminaryScore(bucket[i], q), preliminaryScore(bucket[j], q)
			if left != right {
				return left > right
			}
			if !bucket[i].StartsAt.Equal(bucket[j].StartsAt) {
				return bucket[i].StartsAt.Before(bucket[j].StartsAt)
			}
			return bucket[i].ID < bucket[j].ID
		})
		buckets[key] = bucket
	}
	ordered := make([]domain.Showtime, 0, len(showtimes))
	const dateCoverageRounds = 2
	for round := 0; round < dateCoverageRounds; round++ {
		for _, key := range keys {
			if round < len(buckets[key]) {
				ordered = append(ordered, buckets[key][round])
			}
		}
	}
	remaining := make([]domain.Showtime, 0, len(showtimes)-len(ordered))
	for _, key := range keys {
		bucket := buckets[key]
		if len(bucket) > dateCoverageRounds {
			remaining = append(remaining, bucket[dateCoverageRounds:]...)
		}
	}
	sort.SliceStable(remaining, func(i, j int) bool {
		left, right := maximumPossibleScore(remaining[i], q), maximumPossibleScore(remaining[j], q)
		if left != right {
			return left > right
		}
		if !remaining[i].StartsAt.Equal(remaining[j].StartsAt) {
			return remaining[i].StartsAt.Before(remaining[j].StartsAt)
		}
		return remaining[i].ID < remaining[j].ID
	})
	ordered = append(ordered, remaining...)
	return ordered
}

func showtimeDateKey(startsAt time.Time, q domain.QueryRequest) string {
	if q.Time.Timezone != "" {
		if location, err := time.LoadLocation(q.Time.Timezone); err == nil {
			startsAt = startsAt.In(location)
		}
	}
	return startsAt.Format(time.DateOnly)
}

func distinctShowtimeDates(showtimes []domain.Showtime, q domain.QueryRequest) int {
	dates := map[string]bool{}
	for _, showtime := range showtimes {
		dates[showtimeDateKey(showtime.StartsAt, q)] = true
	}
	return len(dates)
}

func minimumDateCoverage(showtimes []domain.Showtime, q domain.QueryRequest, perDate int) map[string]int {
	counts := map[string]int{}
	for _, showtime := range showtimes {
		counts[showtimeDateKey(showtime.StartsAt, q)]++
	}
	for date, count := range counts {
		if count > perDate {
			counts[date] = perDate
		}
	}
	return counts
}

func dateCoverageSatisfied(successful, targets map[string]int) bool {
	for date, target := range targets {
		if successful[date] < target {
			return false
		}
	}
	return true
}

func dateRangeDays(dates domain.DateConstraint) int {
	start, startErr := time.Parse(time.DateOnly, dates.Start)
	end, endErr := time.Parse(time.DateOnly, dates.End)
	if startErr != nil || endErr != nil || end.Before(start) {
		return 0
	}
	return int(end.Sub(start)/(24*time.Hour)) + 1
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
	inside := clock >= constraint.Start && clock <= constraint.End
	if constraint.Start > constraint.End {
		inside = clock >= constraint.Start || clock <= constraint.End
	}
	switch constraint.Mode {
	case "inside":
		return inside
	case "outside":
		return !inside
	case "before":
		return clock <= constraint.End
	case "after":
		return clock >= constraint.Start
	default:
		return false
	}
}

func applyInventoryPricing(showtime domain.Showtime, inventory domain.Inventory, query domain.QueryRequest) (domain.Showtime, bool) {
	if inventory.TicketPrice != nil {
		perTicket := *inventory.TicketPrice
		if inventory.TicketFee != nil {
			perTicket += *inventory.TicketFee
		}
		total := math.Round(perTicket*float64(query.TicketCount)*100) / 100
		showtime.TotalPrice = &total
		if inventory.Currency != "" {
			showtime.Currency = inventory.Currency
		}
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

func maximumPossibleScore(st domain.Showtime, q domain.QueryRequest) float64 {
	return combinedScore(100, st, q)
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
