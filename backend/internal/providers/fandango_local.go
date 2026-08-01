package providers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"centerseat/backend/internal/domain"
)

const (
	defaultFandangoBaseURL = "https://www.fandango.com"
	fandangoProviderName   = "fandango-local-readonly"
)

var (
	fandangoDigits        = regexp.MustCompile(`^[0-9]+$`)
	fandangoMovieIDSuffix = regexp.MustCompile(`-([0-9]+)$`)
	fandangoPostalCode    = regexp.MustCompile(`\b[0-9]{5}(?:-[0-9]{4})?\b`)
)

type FandangoLocalConfig struct {
	BaseURL        string
	RequestTimeout time.Duration
	MinimumDelay   time.Duration
	MaxConcurrency int
}

type FandangoLocal struct {
	config FandangoLocalConfig
	client *http.Client
	gate   chan struct{}

	throttleMu  sync.Mutex
	lastStart   time.Time
	statusMu    sync.RWMutex
	lastSuccess time.Time

	cacheMu        sync.RWMutex
	movieCache     map[string]cachedFandangoMovie
	discoveryCache map[string]cachedFandangoShowtimes
	inventoryCache map[string]cachedFandangoInventory
}

type cachedFandangoMovie struct {
	movie     fandangoMovie
	expiresAt time.Time
}

type cachedFandangoShowtimes struct {
	showtimes []domain.Showtime
	expiresAt time.Time
}

type cachedFandangoInventory struct {
	inventory domain.Inventory
	expiresAt time.Time
}

func NewFandangoLocal(config FandangoLocalConfig, client *http.Client) (*FandangoLocal, error) {
	config.BaseURL = strings.TrimRight(strings.TrimSpace(config.BaseURL), "/")
	if config.BaseURL == "" {
		config.BaseURL = defaultFandangoBaseURL
	}
	parsed, err := url.Parse(config.BaseURL)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil {
		return nil, fmt.Errorf("FANDANGO_BASE_URL must be an absolute HTTPS URL: %q", config.BaseURL)
	}
	if client == nil && !strings.EqualFold(parsed.Hostname(), "www.fandango.com") {
		return nil, errors.New("FANDANGO_BASE_URL must remain https://www.fandango.com outside provider tests")
	}
	if config.RequestTimeout <= 0 {
		config.RequestTimeout = 8 * time.Second
	}
	if config.MinimumDelay <= 0 {
		config.MinimumDelay = 175 * time.Millisecond
	}
	if config.MaxConcurrency < 1 || config.MaxConcurrency > 4 {
		config.MaxConcurrency = 2
	}
	if client == nil {
		client = &http.Client{Timeout: config.RequestTimeout}
	}
	clientCopy := *client
	clientCopy.CheckRedirect = func(_ *http.Request, _ []*http.Request) error {
		return http.ErrUseLastResponse
	}
	return &FandangoLocal{
		config:         config,
		client:         &clientCopy,
		gate:           make(chan struct{}, config.MaxConcurrency),
		movieCache:     map[string]cachedFandangoMovie{},
		discoveryCache: map[string]cachedFandangoShowtimes{},
		inventoryCache: map[string]cachedFandangoInventory{},
	}, nil
}

func (f *FandangoLocal) Name() string { return fandangoProviderName }

func (f *FandangoLocal) Check(ctx context.Context) error {
	var response fandangoAutocompleteResponse
	if err := f.getJSON(ctx, "/napi/home/autocompleteDesktopSearch", url.Values{"search": {"spider-man"}}, &response); err != nil {
		return err
	}
	f.markSuccess()
	return nil
}

func (f *FandangoLocal) ProviderStatus(kind string) domain.ProviderStatus {
	f.statusMu.RLock()
	last := f.lastSuccess
	f.statusMu.RUnlock()
	status := domain.ProviderStatus{
		Name:         fandangoProviderName,
		Kind:         kind,
		Status:       "healthy",
		Configured:   true,
		Coverage:     "Personal local runtime; Fandango browser showtimes and read-only seat maps",
		LocationMode: "postal_or_coordinates",
		Message:      "GET-only local adapter configured; reservation, token, wallet, payment, and purchase routes are not implemented",
	}
	if last.IsZero() {
		status.Status = "degraded"
		status.Message = "Configured locally; awaiting the first successful read-only request"
	} else {
		status.LastSuccessAt = &last
	}
	return status
}

func (f *FandangoLocal) markSuccess() {
	now := time.Now().UTC()
	f.statusMu.Lock()
	f.lastSuccess = now
	f.statusMu.Unlock()
}

func (f *FandangoLocal) Discover(ctx context.Context, query domain.QueryRequest) ([]domain.Showtime, error) {
	if fandangoPostalCode.FindString(query.Location.Query) == "" && query.Location.Latitude == 0 && query.Location.Longitude == 0 {
		return nil, errors.New("Fandango location requires a 5-digit US ZIP code or browser location coordinates")
	}
	movie, err := f.movieForQuery(ctx, query)
	if err != nil {
		return nil, err
	}
	start, err := time.Parse(time.DateOnly, query.Dates.Start)
	if err != nil {
		return nil, err
	}
	end, err := time.Parse(time.DateOnly, query.Dates.End)
	if err != nil {
		return nil, err
	}
	dates := []time.Time{}
	for day := start; !day.After(end); day = day.AddDate(0, 0, 1) {
		dates = append(dates, day)
	}

	type result struct {
		index int
		items []domain.Showtime
		err   error
	}
	results := make(chan result, len(dates))
	var wait sync.WaitGroup
	for index, date := range dates {
		wait.Add(1)
		go func(index int, date time.Time) {
			defer wait.Done()
			items, err := f.discoverDate(ctx, movie, date.Format(time.DateOnly), query)
			results <- result{index: index, items: items, err: err}
		}(index, date)
	}
	wait.Wait()
	close(results)

	ordered := make([][]domain.Showtime, len(dates))
	var failures []error
	for result := range results {
		if result.err != nil {
			failures = append(failures, result.err)
			continue
		}
		ordered[result.index] = result.items
	}
	if len(failures) == len(dates) && len(failures) > 0 {
		return nil, fmt.Errorf("all Fandango date queries failed: %w", failures[0])
	}
	showtimes := []domain.Showtime{}
	seen := map[string]bool{}
	for _, items := range ordered {
		for _, showtime := range items {
			key := showtime.SeatLayoutID
			if key == "" {
				key = showtime.ID
			}
			if key == "" || seen[key] {
				continue
			}
			seen[key] = true
			showtimes = append(showtimes, showtime)
		}
	}
	f.markSuccess()
	return showtimes, nil
}

func (f *FandangoLocal) movieForQuery(ctx context.Context, query domain.QueryRequest) (fandangoMovie, error) {
	if query.MovieID == "" {
		return f.resolveMovie(ctx, query.MovieQuery)
	}
	if !fandangoDigits.MatchString(query.MovieID) {
		return fandangoMovie{}, errors.New("Fandango movie_id must be numeric")
	}
	return fandangoMovie{ID: query.MovieID, Name: query.MovieQuery}, nil
}

func (f *FandangoLocal) SuggestMovies(ctx context.Context, query string, limit int) ([]domain.MovieSuggestion, error) {
	query = strings.TrimSpace(query)
	if len(query) < 2 {
		return []domain.MovieSuggestion{}, nil
	}
	if limit < 1 || limit > 10 {
		limit = 6
	}
	var response fandangoAutocompleteResponse
	if err := f.getJSON(ctx, "/napi/home/autocompleteDesktopSearch", url.Values{"search": {query}}, &response); err != nil {
		return nil, err
	}
	type rankedMovie struct {
		movie fandangoMovie
		score int
		order int
	}
	ranked := make([]rankedMovie, 0, len(response.ResultsByType.Movies.Items))
	for index, candidate := range response.ResultsByType.Movies.Items {
		movie, valid := normalizeFandangoMovie(candidate)
		if valid {
			ranked = append(ranked, rankedMovie{movie: movie, score: fandangoTitleScore(query, movie.Name), order: index})
		}
	}
	sort.SliceStable(ranked, func(i, j int) bool {
		if ranked[i].score == ranked[j].score {
			return ranked[i].order < ranked[j].order
		}
		return ranked[i].score > ranked[j].score
	})
	if len(ranked) > limit {
		ranked = ranked[:limit]
	}
	suggestions := make([]domain.MovieSuggestion, 0, len(ranked))
	for _, candidate := range ranked {
		year := ""
		if len(candidate.movie.ReleaseDate) >= 4 {
			year = candidate.movie.ReleaseDate[:4]
		}
		suggestions = append(suggestions, domain.MovieSuggestion{
			ID: candidate.movie.ID, Title: candidate.movie.Name,
			ReleaseDate: candidate.movie.ReleaseDate, Year: year,
		})
	}
	f.markSuccess()
	return suggestions, nil
}

func (f *FandangoLocal) resolveMovie(ctx context.Context, query string) (fandangoMovie, error) {
	normalized := normalizeText(query)
	if normalized == "" {
		return fandangoMovie{}, errors.New("Fandango movie search requires a title")
	}
	now := time.Now()
	f.cacheMu.RLock()
	cached, ok := f.movieCache[normalized]
	f.cacheMu.RUnlock()
	if ok && now.Before(cached.expiresAt) {
		return cached.movie, nil
	}

	suggestions, err := f.SuggestMovies(ctx, query, 10)
	if err != nil {
		return fandangoMovie{}, err
	}
	bestScore := -1 << 30
	var best fandangoMovie
	for _, candidate := range suggestions {
		movie := fandangoMovie{ID: candidate.ID, Name: candidate.Title, ReleaseDate: candidate.ReleaseDate}
		score := fandangoTitleScore(query, candidate.Title)
		if score > bestScore {
			bestScore = score
			best = movie
		}
	}
	if best.ID == "" {
		return fandangoMovie{}, fmt.Errorf("Fandango returned no movie match for %q", query)
	}
	f.cacheMu.Lock()
	f.movieCache[normalized] = cachedFandangoMovie{movie: best, expiresAt: now.Add(12 * time.Hour)}
	f.cacheMu.Unlock()
	f.markSuccess()
	return best, nil
}

func (f *FandangoLocal) discoverDate(ctx context.Context, movie fandangoMovie, date string, query domain.QueryRequest) ([]domain.Showtime, error) {
	locationKey, parameters := fandangoLocationParameters(query.Location)
	cacheKey := strings.Join([]string{movie.ID, date, locationKey}, "|")
	now := time.Now()
	f.cacheMu.RLock()
	cached, ok := f.discoveryCache[cacheKey]
	f.cacheMu.RUnlock()
	if ok && now.Before(cached.expiresAt) {
		return cloneShowtimes(cached.showtimes), nil
	}
	parameters.Set("isdesktop", "true")
	parameters.Set("isDesktopMOP", "true")
	parameters.Set("partnerRestrictedTicketing", "false")
	path := "/napi/theaterShowtimeGroupings/" + movie.ID + "/" + date
	var response fandangoShowtimeGroupingsResponse
	if err := f.getJSON(ctx, path, parameters, &response); err != nil {
		return nil, err
	}
	showtimes := normalizeFandangoShowtimes(response, movie, query)
	f.cacheMu.Lock()
	f.discoveryCache[cacheKey] = cachedFandangoShowtimes{
		showtimes: cloneShowtimes(showtimes),
		expiresAt: now.Add(2 * time.Minute),
	}
	f.cacheMu.Unlock()
	return showtimes, nil
}

func (f *FandangoLocal) Supports(showtime domain.Showtime) bool {
	return showtime.InventoryProvider == fandangoProviderName && showtime.SeatLayoutID != ""
}

func (f *FandangoLocal) MaxConcurrentInventoryReads() int {
	return f.config.MaxConcurrency
}

func (f *FandangoLocal) GetAvailability(ctx context.Context, showtime domain.Showtime, final bool) (domain.Inventory, error) {
	if !f.Supports(showtime) {
		return domain.Inventory{}, errors.New("Fandango local inventory does not support this showtime")
	}
	if !validFandangoOpaqueID(showtime.SeatLayoutID) {
		return domain.Inventory{}, errors.New("Fandango showtime hash was not safe to request")
	}
	now := time.Now()
	if !final {
		f.cacheMu.RLock()
		cached, ok := f.inventoryCache[showtime.SeatLayoutID]
		f.cacheMu.RUnlock()
		if ok && now.Before(cached.expiresAt) {
			return cloneInventory(cached.inventory), nil
		}
	}
	var response json.RawMessage
	if err := f.getJSON(ctx, "/napi/seatMap/"+showtime.SeatLayoutID, nil, &response); err != nil {
		return domain.Inventory{}, err
	}
	seatMap, err := decodeFandangoSeatMap(response)
	if err != nil {
		return domain.Inventory{}, err
	}
	inventory, err := normalizeFandangoInventory(showtime, seatMap, now.UTC())
	if err != nil {
		return domain.Inventory{}, err
	}
	f.cacheMu.Lock()
	f.inventoryCache[showtime.SeatLayoutID] = cachedFandangoInventory{
		inventory: cloneInventory(inventory),
		expiresAt: now.Add(2 * time.Second),
	}
	f.cacheMu.Unlock()
	f.markSuccess()
	return inventory, nil
}

func (f *FandangoLocal) getJSON(ctx context.Context, path string, query url.Values, destination any) error {
	if !allowedFandangoReadPath(path) {
		return fmt.Errorf("Fandango local adapter refused non-allowlisted path %q", path)
	}
	endpoint := f.config.BaseURL + path
	if len(query) > 0 {
		endpoint += "?" + query.Encode()
	}
	for attempt := 0; attempt < 3; attempt++ {
		if err := f.waitForRequestSlot(ctx); err != nil {
			return err
		}
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
		if err != nil {
			f.releaseRequestSlot()
			return err
		}
		request.Header.Set("Accept", "application/json")
		request.Header.Set("Accept-Language", "en-US,en;q=0.9")
		request.Header.Set("Referer", f.config.BaseURL+"/")
		request.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 Chrome/138 Safari/537.36")
		request.Header.Set("X-Requested-With", "XMLHttpRequest")
		response, err := f.client.Do(request)
		f.releaseRequestSlot()
		if err != nil {
			return fmt.Errorf("Fandango read failed: %w", err)
		}
		body, readErr := io.ReadAll(io.LimitReader(response.Body, 16<<20))
		_ = response.Body.Close()
		if readErr != nil {
			return readErr
		}
		if retryableReadStatus(response.StatusCode) && attempt < 2 {
			delay := time.Duration(150*(1<<attempt))*time.Millisecond + retryJitter()
			select {
			case <-time.After(delay):
				continue
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		if response.StatusCode < 200 || response.StatusCode >= 300 {
			return fmt.Errorf("Fandango read returned HTTP %d", response.StatusCode)
		}
		if destination == nil || len(body) == 0 {
			return nil
		}
		if err := json.Unmarshal(body, destination); err != nil {
			return fmt.Errorf("decode Fandango read response: %w", err)
		}
		return nil
	}
	return errors.New("Fandango read exhausted retries")
}

func (f *FandangoLocal) waitForRequestSlot(ctx context.Context) error {
	select {
	case f.gate <- struct{}{}:
	case <-ctx.Done():
		return ctx.Err()
	}
	f.throttleMu.Lock()
	wait := time.Until(f.lastStart.Add(f.config.MinimumDelay))
	if wait > 0 {
		timer := time.NewTimer(wait)
		select {
		case <-timer.C:
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			f.throttleMu.Unlock()
			f.releaseRequestSlot()
			return ctx.Err()
		}
	}
	f.lastStart = time.Now()
	f.throttleMu.Unlock()
	return nil
}

func (f *FandangoLocal) releaseRequestSlot() { <-f.gate }

func retryableReadStatus(status int) bool {
	return status == http.StatusInternalServerError ||
		status == http.StatusTooManyRequests ||
		status == http.StatusBadGateway ||
		status == http.StatusServiceUnavailable ||
		status == http.StatusGatewayTimeout
}

func allowedFandangoReadPath(path string) bool {
	if path == "/napi/home/autocompleteDesktopSearch" {
		return true
	}
	parts := strings.Split(strings.TrimPrefix(path, "/"), "/")
	if len(parts) == 4 && parts[0] == "napi" && parts[1] == "theaterShowtimeGroupings" {
		return fandangoDigits.MatchString(parts[2]) && isDateOnly(parts[3])
	}
	return len(parts) == 3 && parts[0] == "napi" && parts[1] == "seatMap" && validFandangoOpaqueID(parts[2])
}

func isDateOnly(value string) bool {
	_, err := time.Parse(time.DateOnly, value)
	return err == nil
}

func validFandangoOpaqueID(value string) bool {
	value = strings.TrimSpace(value)
	return value != "" && len(value) <= 512 && !strings.ContainsAny(value, "/\\?#") && value != "." && value != ".."
}

type fandangoAutocompleteResponse struct {
	ResultsByType struct {
		Movies struct {
			Items []fandangoAutocompleteMovie `json:"items"`
		} `json:"movies"`
	} `json:"resultsByType"`
}

type fandangoAutocompleteMovie struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Link        string `json:"link"`
	ReleaseDate string `json:"releaseDate"`
}

type fandangoMovie struct {
	ID          string
	Name        string
	Slug        string
	ReleaseDate string
}

func normalizeFandangoMovie(source fandangoAutocompleteMovie) (fandangoMovie, bool) {
	name := strings.TrimSpace(source.Name)
	parsed, err := url.Parse(strings.TrimSpace(source.Link))
	if err != nil || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return fandangoMovie{}, false
	}
	if parsed.IsAbs() && !strings.EqualFold(parsed.Hostname(), "www.fandango.com") {
		return fandangoMovie{}, false
	}
	segments := strings.Split(strings.Trim(parsed.Path, "/"), "/")
	if len(segments) == 0 || segments[0] == "" {
		return fandangoMovie{}, false
	}
	slug := segments[0]
	id := strings.TrimSpace(source.ID)
	if !fandangoDigits.MatchString(id) {
		match := fandangoMovieIDSuffix.FindStringSubmatch(slug)
		if len(match) != 2 {
			return fandangoMovie{}, false
		}
		id = match[1]
	}
	if name == "" {
		return fandangoMovie{}, false
	}
	return fandangoMovie{ID: id, Name: name, Slug: slug, ReleaseDate: strings.TrimSpace(source.ReleaseDate)}, true
}

func fandangoTitleScore(query, candidate string) int {
	query = normalizeText(query)
	candidate = normalizeText(candidate)
	compactQuery := strings.ReplaceAll(query, " ", "")
	compactCandidate := strings.ReplaceAll(candidate, " ", "")
	switch {
	case query == "" || candidate == "":
		return -1
	case query == candidate:
		return 1000
	case compactQuery == compactCandidate:
		return 950
	case strings.HasPrefix(compactCandidate, compactQuery):
		return 850 - (len(compactCandidate) - len(compactQuery))
	case strings.Contains(compactCandidate, compactQuery):
		return 700 - (len(compactCandidate) - len(compactQuery))
	case strings.HasPrefix(candidate, query):
		return 800 - (len(candidate) - len(query))
	case strings.Contains(candidate, query):
		return 600 - (len(candidate) - len(query))
	case strings.Contains(query, candidate):
		return 400 - (len(query) - len(candidate))
	default:
		return -1
	}
}

type fandangoShowtimeGroupingsResponse struct {
	TheaterShowtimes struct {
		Theaters []fandangoTheater `json:"theaters"`
	} `json:"theaterShowtimes"`
}

type fandangoTheater struct {
	ID        string  `json:"id"`
	Name      string  `json:"name"`
	Distance  float64 `json:"distance"`
	Amenities []struct {
		Name string `json:"name"`
	} `json:"amenities"`
	Variants []struct {
		FilmFormatHeader string                 `json:"filmFormatHeader"`
		AmenityGroups    []fandangoAmenityGroup `json:"amenityGroups"`
	} `json:"variants"`
}

type fandangoAmenityGroup struct {
	AmenityString      string `json:"amenityString"`
	HasReservedSeating bool   `json:"hasReservedSeating"`
	Amenities          []struct {
		Name string `json:"name"`
	} `json:"amenities"`
	Showtimes []fandangoShowtime `json:"showtimes"`
}

type fandangoShowtime struct {
	ID                   json.RawMessage   `json:"id"`
	Date                 string            `json:"date"`
	DateLocal            string            `json:"dateLocal"`
	DateUTC              string            `json:"dateUtc"`
	FilmFormat           []json.RawMessage `json:"filmFormat"`
	ShowtimeHashCode     string            `json:"showtimeHashCode"`
	TicketingJumpPageURL string            `json:"ticketingJumpPageURL"`
	Type                 string            `json:"type"`
	Expired              bool              `json:"expired"`
}

func normalizeFandangoShowtimes(response fandangoShowtimeGroupingsResponse, movie fandangoMovie, query domain.QueryRequest) []domain.Showtime {
	result := []domain.Showtime{}
	for _, theater := range response.TheaterShowtimes.Theaters {
		for _, variant := range theater.Variants {
			for _, group := range variant.AmenityGroups {
				attributes := []string{variant.FilmFormatHeader, group.AmenityString}
				for _, amenity := range theater.Amenities {
					attributes = append(attributes, amenity.Name)
				}
				for _, amenity := range group.Amenities {
					attributes = append(attributes, amenity.Name)
				}
				for _, source := range group.Showtimes {
					if source.Expired {
						continue
					}
					startsAt, err := parseFandangoShowtime(source, query.Time.Timezone)
					if err != nil {
						continue
					}
					showtimeAttributes := append([]string(nil), attributes...)
					showtimeAttributes = append(showtimeAttributes, fandangoFilmFormatNames(source.FilmFormat)...)
					showtimeAttributes = append(showtimeAttributes, source.Type)
					format, amenities, captions, audio := classifyAttributes(showtimeAttributes, false)
					reserved := group.HasReservedSeating && validFandangoOpaqueID(source.ShowtimeHashCode)
					id := strings.Trim(string(source.ID), `" `)
					if id == "" {
						id = source.ShowtimeHashCode
					}
					showtime := domain.Showtime{
						ID:               id,
						MovieTitle:       movie.Name,
						VenueName:        theater.Name,
						StartsAt:         startsAt,
						Format:           format,
						DistanceMiles:    theater.Distance,
						Amenities:        amenities,
						Captions:         captions,
						AudioDescription: audio,
						ReservedSeating:  reserved,
						BookingURL:       validFandangoCheckoutURL(source.TicketingJumpPageURL),
						SiteID:           theater.ID,
					}
					if reserved {
						showtime.InventoryProvider = fandangoProviderName
						showtime.SeatLayoutID = source.ShowtimeHashCode
					}
					result = append(result, showtime)
				}
			}
		}
	}
	return result
}

func fandangoFilmFormatNames(values []json.RawMessage) []string {
	result := make([]string, 0, len(values))
	for _, value := range values {
		var text string
		if json.Unmarshal(value, &text) == nil && strings.TrimSpace(text) != "" {
			result = append(result, text)
			continue
		}
		var object struct {
			FilterName string `json:"filterName"`
		}
		if json.Unmarshal(value, &object) == nil && strings.TrimSpace(object.FilterName) != "" {
			result = append(result, object.FilterName)
		}
	}
	return result
}

func parseFandangoShowtime(source fandangoShowtime, timezone string) (time.Time, error) {
	for _, value := range []string{source.DateUTC, source.Date} {
		if parsed, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(value)); err == nil {
			return parsed, nil
		}
	}
	location := time.Local
	if timezone != "" {
		if parsed, err := time.LoadLocation(timezone); err == nil {
			location = parsed
		}
	}
	for _, layout := range []string{"2006-01-02T15:04:05", "2006-01-02 15:04:05", time.RFC3339Nano} {
		if parsed, err := time.ParseInLocation(layout, strings.TrimSpace(source.DateLocal), location); err == nil {
			return parsed, nil
		}
	}
	return time.Time{}, errors.New("Fandango showtime omitted a parseable start time")
}

func fandangoLocationParameters(location domain.LocationConstraint) (string, url.Values) {
	parameters := url.Values{}
	if postal := fandangoPostalCode.FindString(location.Query); postal != "" {
		parameters.Set("postalCode", postal)
		parameters.Set("zip", postal)
		return "zip:" + postal, parameters
	}
	parameters.Set("lat", strconv.FormatFloat(location.Latitude, 'f', 6, 64))
	parameters.Set("long", strconv.FormatFloat(location.Longitude, 'f', 6, 64))
	return "geo:" + parameters.Get("lat") + "," + parameters.Get("long"), parameters
}

func validFandangoCheckoutURL(value string) string {
	parsed, err := url.Parse(strings.TrimSpace(value))
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil {
		return ""
	}
	host := strings.ToLower(parsed.Hostname())
	if host != "fandango.com" && !strings.HasSuffix(host, ".fandango.com") {
		return ""
	}
	return parsed.String()
}

type fandangoSeatMap struct {
	TotalWidth  float64            `json:"totalWidth"`
	TotalHeight float64            `json:"totalHeight"`
	Seats       []fandangoSeat     `json:"seats"`
	Areas       []fandangoSeatArea `json:"areas"`
}

type fandangoSeat struct {
	ID     string  `json:"id"`
	Row    int     `json:"row"`
	Column int     `json:"column"`
	X      float64 `json:"x"`
	Y      float64 `json:"y"`
	Width  float64 `json:"width"`
	Height float64 `json:"height"`
	Type   string  `json:"type"`
	Status string  `json:"status"`
}

type fandangoSeatArea struct {
	TicketInfo []struct {
		Description string `json:"desc"`
		Price       string `json:"price"`
		Fee         string `json:"fee"`
	} `json:"ticketInfo"`
}

func decodeFandangoSeatMap(raw json.RawMessage) (fandangoSeatMap, error) {
	var direct fandangoSeatMap
	if err := json.Unmarshal(raw, &direct); err == nil && len(direct.Seats) > 0 {
		return direct, nil
	}
	var wrapped struct {
		Data fandangoSeatMap `json:"data"`
	}
	if err := json.Unmarshal(raw, &wrapped); err == nil && len(wrapped.Data.Seats) > 0 {
		return wrapped.Data, nil
	}
	return fandangoSeatMap{}, errors.New("Fandango seat-map response did not contain a usable seat array")
}

func normalizeFandangoInventory(showtime domain.Showtime, seatMap fandangoSeatMap, observedAt time.Time) (domain.Inventory, error) {
	if len(seatMap.Seats) == 0 {
		return domain.Inventory{}, errors.New("Fandango seat map contained no seats")
	}
	minX, minY := math.MaxFloat64, math.MaxFloat64
	maxX, maxY := -math.MaxFloat64, -math.MaxFloat64
	for _, seat := range seatMap.Seats {
		centerX := seat.X + seat.Width/2
		centerY := seat.Y + seat.Height/2
		minX = math.Min(minX, centerX)
		maxX = math.Max(maxX, centerX)
		minY = math.Min(minY, centerY)
		maxY = math.Max(maxY, centerY)
	}
	spanX := math.Max(1, maxX-minX)
	spanY := math.Max(1, maxY-minY)
	seats := make([]domain.Seat, 0, len(seatMap.Seats))
	for _, source := range seatMap.Seats {
		if strings.TrimSpace(source.ID) == "" {
			continue
		}
		row := leadingLetters(source.ID)
		if row == "" {
			row = strconv.Itoa(source.Row)
		}
		index := source.Column
		if number, ok := seatNumber(source.ID); ok {
			index = number
		}
		seats = append(seats, domain.Seat{
			ID:     source.ID,
			Label:  source.ID,
			Row:    row,
			Index:  index,
			X:      (source.X + source.Width/2 - minX) / spanX,
			Y:      (source.Y + source.Height/2 - minY) / spanY,
			Type:   normalizeFandangoSeatType(source.Type),
			Status: normalizeFandangoSeatStatus(source.Status),
		})
	}
	if len(seats) == 0 {
		return domain.Inventory{}, errors.New("Fandango seat map contained no identifiable seats")
	}
	ticketPrice, ticketFee := preferredFandangoTicketPrice(seatMap.Areas)
	return domain.Inventory{
		ShowtimeID:  showtime.ID,
		Seats:       seats,
		Confidence:  "exact_coordinates",
		ObservedAt:  observedAt,
		FreshFor:    2 * time.Second,
		TicketPrice: ticketPrice,
		TicketFee:   ticketFee,
		Currency:    "USD",
	}, nil
}

func normalizeFandangoSeatType(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "wheelchair":
		return "wheelchair"
	case "companion":
		return "companion"
	case "recliner", "premium":
		return "recliner"
	case "sofa", "loveseat":
		return "sofa"
	default:
		return "standard"
	}
}

func normalizeFandangoSeatStatus(value string) string {
	switch strings.ToUpper(strings.TrimSpace(value)) {
	case "A":
		return "available"
	case "R":
		return "sold"
	case "H":
		return "held"
	default:
		return "house"
	}
}

func preferredFandangoTicketPrice(areas []fandangoSeatArea) (*float64, *float64) {
	type candidate struct {
		description string
		price       float64
		fee         float64
	}
	candidates := []candidate{}
	for _, area := range areas {
		for _, ticket := range area.TicketInfo {
			price, priceOK := parseFandangoMoney(ticket.Price)
			fee, feeOK := parseFandangoMoney(ticket.Fee)
			if !priceOK {
				continue
			}
			if !feeOK {
				fee = 0
			}
			candidates = append(candidates, candidate{
				description: strings.ToLower(ticket.Description),
				price:       price,
				fee:         fee,
			})
		}
	}
	if len(candidates) == 0 {
		return nil, nil
	}
	selected := candidates[0]
	for _, candidate := range candidates {
		if strings.Contains(candidate.description, "adult") {
			selected = candidate
			break
		}
	}
	return &selected.price, &selected.fee
}

func parseFandangoMoney(value string) (float64, bool) {
	cleaned := strings.Map(func(character rune) rune {
		if (character >= '0' && character <= '9') || character == '.' || character == '-' {
			return character
		}
		return -1
	}, value)
	if cleaned == "" {
		return 0, false
	}
	amount, err := strconv.ParseFloat(cleaned, 64)
	return amount, err == nil && amount >= 0
}

func cloneShowtimes(source []domain.Showtime) []domain.Showtime {
	result := append([]domain.Showtime(nil), source...)
	for index := range result {
		result[index].Amenities = append([]string(nil), source[index].Amenities...)
	}
	return result
}

func cloneInventory(source domain.Inventory) domain.Inventory {
	copy := source
	copy.Seats = append([]domain.Seat(nil), source.Seats...)
	if source.TicketPrice != nil {
		value := *source.TicketPrice
		copy.TicketPrice = &value
	}
	if source.TicketFee != nil {
		value := *source.TicketFee
		copy.TicketFee = &value
	}
	return copy
}
