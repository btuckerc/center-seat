package providers

import (
	"bytes"
	"context"
	cryptorand "crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"centerseat/backend/internal/domain"
)

const atomMaximumRadiusMiles = 80 / 1.609344

type AtomConfig struct {
	BaseURL        string
	APIKey         string
	PartnerID      string
	RequestTimeout time.Duration
}

type Atom struct {
	config AtomConfig
	client *http.Client

	statusMu    sync.RWMutex
	lastSuccess time.Time
}

func NewAtom(config AtomConfig, client *http.Client) (*Atom, error) {
	config.BaseURL = strings.TrimRight(strings.TrimSpace(config.BaseURL), "/")
	config.APIKey = strings.TrimSpace(config.APIKey)
	config.PartnerID = strings.TrimSpace(config.PartnerID)
	if config.BaseURL == "" {
		config.BaseURL = "https://api.atomtickets.com"
	}
	if config.APIKey == "" {
		return nil, errors.New("missing required Atom configuration: ATOM_API_KEY")
	}
	parsed, err := url.Parse(config.BaseURL)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" {
		return nil, fmt.Errorf("ATOM_API_BASE_URL must be an absolute HTTPS URL: %q", config.BaseURL)
	}
	if config.RequestTimeout <= 0 {
		config.RequestTimeout = 8 * time.Second
	}
	if client == nil {
		client = &http.Client{Timeout: config.RequestTimeout}
	}
	return &Atom{config: config, client: client}, nil
}

func (a *Atom) Name() string { return "atom-tickets-partner-api" }

func (a *Atom) ProviderStatus(kind string) domain.ProviderStatus {
	a.statusMu.RLock()
	last := a.lastSuccess
	a.statusMu.RUnlock()
	status := domain.ProviderStatus{
		Name: a.Name(), Kind: kind, Status: "healthy", Configured: true,
		Coverage: "Atom Tickets partner network", LocationMode: "coordinates", Message: "Partner API key configured",
	}
	if last.IsZero() {
		status.Status = "degraded"
		status.Message = "Configured; awaiting first successful Atom request"
	} else {
		status.LastSuccessAt = &last
	}
	return status
}

func (a *Atom) markSuccess() {
	now := time.Now().UTC()
	a.statusMu.Lock()
	a.lastSuccess = now
	a.statusMu.Unlock()
}

func (a *Atom) Check(ctx context.Context) error {
	if err := a.doJSON(ctx, http.MethodGet, "/partner/ping", nil, nil, nil); err != nil {
		return err
	}
	a.markSuccess()
	return nil
}

func (a *Atom) Discover(ctx context.Context, query domain.QueryRequest) ([]domain.Showtime, error) {
	if query.Location.Latitude == 0 && query.Location.Longitude == 0 {
		return nil, errors.New("Atom venue discovery requires latitude and longitude; use precise location")
	}
	if query.Location.RadiusMiles > atomMaximumRadiusMiles {
		return nil, fmt.Errorf("Atom supports a maximum search radius of %.1f miles", atomMaximumRadiusMiles)
	}
	venues, err := a.nearbyVenues(ctx, query.Location)
	if err != nil {
		return nil, err
	}
	if len(venues) == 0 {
		a.markSuccess()
		return []domain.Showtime{}, nil
	}
	start, err := time.Parse(time.DateOnly, query.Dates.Start)
	if err != nil {
		return nil, err
	}
	end, err := time.Parse(time.DateOnly, query.Dates.End)
	if err != nil {
		return nil, err
	}

	type result struct {
		payload atomShowtimesForVenuesResponse
		err     error
	}
	requests := []atomShowtimesForVenuesRequest{}
	venueIDs := make([]string, 0, len(venues))
	for _, venue := range venues {
		venueIDs = append(venueIDs, venue.ID)
	}
	for cursor := start; !cursor.After(end); {
		exclusiveEnd := cursor.AddDate(0, 0, 7)
		if exclusiveEnd.After(end.AddDate(0, 0, 1)) {
			exclusiveEnd = end.AddDate(0, 0, 1)
		}
		for offset := 0; offset < len(venueIDs); offset += 100 {
			limit := offset + 100
			if limit > len(venueIDs) {
				limit = len(venueIDs)
			}
			requests = append(requests, atomShowtimesForVenuesRequest{
				VenueIDs: append([]string(nil), venueIDs[offset:limit]...),
				LocalDateBounds: atomLocalDateBounds{
					Start: cursor.Format(time.DateOnly) + "T00:00:00",
					End:   exclusiveEnd.Format(time.DateOnly) + "T00:00:00",
				},
				IncludeProductionDetails: true,
			})
		}
		cursor = exclusiveEnd
	}

	results := make(chan result, len(requests))
	sem := make(chan struct{}, 6)
	var wg sync.WaitGroup
	for _, request := range requests {
		request := request
		wg.Add(1)
		go func() {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-ctx.Done():
				results <- result{err: ctx.Err()}
				return
			}
			var payload atomShowtimesForVenuesResponse
			err := a.doJSON(ctx, http.MethodPost, "/partner/v1/showtime/details/forVenues", nil, request, &payload)
			results <- result{payload: payload, err: err}
		}()
	}
	wg.Wait()
	close(results)

	venueByID := make(map[string]atomVenue, len(venues))
	for _, venue := range venues {
		venueByID[venue.ID] = venue
	}
	unique := map[string]domain.Showtime{}
	for item := range results {
		if item.err != nil {
			return nil, item.err
		}
		for _, showtime := range normalizeAtomShowtimes(item.payload, venueByID, query) {
			unique[showtime.ID] = showtime
		}
	}
	showtimes := make([]domain.Showtime, 0, len(unique))
	for _, showtime := range unique {
		showtimes = append(showtimes, showtime)
	}
	sort.Slice(showtimes, func(i, j int) bool { return showtimes[i].StartsAt.Before(showtimes[j].StartsAt) })
	a.markSuccess()
	return showtimes, nil
}

func (a *Atom) Supports(showtime domain.Showtime) bool {
	return showtime.InventoryProvider == a.Name()
}

func (a *Atom) GetAvailability(ctx context.Context, showtime domain.Showtime, _ bool) (domain.Inventory, error) {
	query := url.Values{"showtimeId": {showtime.ID}}
	var payload atomAuditoriumDiscoveryResponse
	if err := a.doJSON(ctx, http.MethodGet, "/ordering/v1/discovery/auditoriums", query, nil, &payload); err != nil {
		return domain.Inventory{}, err
	}
	if !payload.SeatMapAvailable || len(payload.Seats) == 0 {
		return domain.Inventory{}, errors.New("Atom did not return a reserved-seat map for this showtime")
	}
	seats := normalizeAtomSeats(payload.Seats)
	if len(seats) == 0 {
		return domain.Inventory{}, errors.New("Atom seat map contained no usable seats")
	}
	a.markSuccess()
	return domain.Inventory{
		ShowtimeID: showtime.ID, Seats: seats, Confidence: "row_geometry",
		ObservedAt: time.Now().UTC(), FreshFor: 5 * time.Second,
	}, nil
}

func (a *Atom) nearbyVenues(ctx context.Context, location domain.LocationConstraint) ([]atomVenue, error) {
	query := url.Values{
		"lat":      {strconv.FormatFloat(location.Latitude, 'f', 6, 64)},
		"lon":      {strconv.FormatFloat(location.Longitude, 'f', 6, 64)},
		"radius":   {strconv.FormatFloat(location.RadiusMiles*1.609344, 'f', 3, 64)},
		"pageSize": {"100"},
	}
	var first atomVenueDetailsResponse
	if err := a.doJSON(ctx, http.MethodGet, "/partner/v1/venue/details/byLocation", query, nil, &first); err != nil {
		return nil, err
	}
	all := append([]atomVenueDetails(nil), first.VenueDetails...)
	current := first.PageInfo.Page
	total := first.PageInfo.TotalPages
	if total > 1 {
		startPage := 1
		endPage := total - 1
		if current > 0 {
			startPage = 2
			endPage = total
		}
		for page := startPage; page <= endPage; page++ {
			pageQuery := cloneValues(query)
			pageQuery.Set("page", strconv.Itoa(page))
			var payload atomVenueDetailsResponse
			if err := a.doJSON(ctx, http.MethodGet, "/partner/v1/venue/details/byLocation", pageQuery, nil, &payload); err != nil {
				return nil, err
			}
			all = append(all, payload.VenueDetails...)
		}
	}
	unique := map[string]atomVenue{}
	for _, details := range all {
		venue := details.Venue
		if venue.ID == "" || (venue.IsActive != nil && !*venue.IsActive) || (venue.Properties.Supported != nil && !*venue.Properties.Supported) {
			continue
		}
		venue.DistanceMiles = details.KMDistance / 1.609344
		if venue.DistanceMiles == 0 && (venue.Address.Lat != 0 || venue.Address.Lon != 0) {
			venue.DistanceMiles = haversineMiles(location.Latitude, location.Longitude, venue.Address.Lat, venue.Address.Lon)
		}
		unique[venue.ID] = venue
	}
	venues := make([]atomVenue, 0, len(unique))
	for _, venue := range unique {
		venues = append(venues, venue)
	}
	sort.Slice(venues, func(i, j int) bool { return venues[i].DistanceMiles < venues[j].DistanceMiles })
	return venues, nil
}

func (a *Atom) doJSON(ctx context.Context, method, path string, query url.Values, requestBody, destination any) error {
	endpoint := a.config.BaseURL + path
	if len(query) > 0 {
		endpoint += "?" + query.Encode()
	}
	var encoded []byte
	var err error
	if requestBody != nil {
		encoded, err = json.Marshal(requestBody)
		if err != nil {
			return err
		}
	}
	for attempt := 0; attempt < 3; attempt++ {
		var body io.Reader
		if encoded != nil {
			body = bytes.NewReader(encoded)
		}
		request, err := http.NewRequestWithContext(ctx, method, endpoint, body)
		if err != nil {
			return err
		}
		request.Header.Set("Accept", "application/json")
		request.Header.Set("x-api-key", a.config.APIKey)
		request.Header.Set("User-Agent", "CenterSeat/1.0")
		if a.config.PartnerID != "" {
			request.Header.Set("X-Atom-Partner", a.config.PartnerID)
		}
		if encoded != nil {
			request.Header.Set("Content-Type", "application/json")
		}
		response, err := a.client.Do(request)
		if err != nil {
			return fmt.Errorf("Atom request failed: %w", err)
		}
		responseBody, readErr := io.ReadAll(io.LimitReader(response.Body, 24<<20))
		_ = response.Body.Close()
		if readErr != nil {
			return readErr
		}
		if response.StatusCode == http.StatusTooManyRequests || response.StatusCode == http.StatusBadGateway || response.StatusCode == http.StatusServiceUnavailable || response.StatusCode == http.StatusGatewayTimeout {
			if attempt < 2 {
				delay := time.Duration(100*(1<<attempt))*time.Millisecond + retryJitter()
				select {
				case <-time.After(delay):
					continue
				case <-ctx.Done():
					return ctx.Err()
				}
			}
		}
		if response.StatusCode < 200 || response.StatusCode >= 300 {
			return fmt.Errorf("Atom returned HTTP %d", response.StatusCode)
		}
		if destination == nil || len(responseBody) == 0 {
			return nil
		}
		if err := json.Unmarshal(responseBody, destination); err != nil {
			return fmt.Errorf("decode Atom response: %w", err)
		}
		return nil
	}
	return errors.New("Atom request retries exhausted")
}

func retryJitter() time.Duration {
	var value [1]byte
	if _, err := cryptorand.Read(value[:]); err != nil {
		return 0
	}
	return time.Duration(value[0]%50) * time.Millisecond
}

type atomPageInfo struct {
	Page       int `json:"page"`
	TotalPages int `json:"totalPages"`
}

type atomVenueProperties struct {
	Supported *bool `json:"supported"`
}

type atomAddress struct {
	Line   string  `json:"line"`
	City   string  `json:"city"`
	State  string  `json:"state"`
	Postal string  `json:"postal"`
	Lat    float64 `json:"lat"`
	Lon    float64 `json:"lon"`
}

type atomVenue struct {
	ID            string              `json:"id"`
	Name          string              `json:"name"`
	Address       atomAddress         `json:"address"`
	Properties    atomVenueProperties `json:"properties"`
	IsActive      *bool               `json:"isActive"`
	DistanceMiles float64             `json:"-"`
}

type atomVenueDetails struct {
	Venue      atomVenue `json:"venue"`
	KMDistance float64   `json:"kmDistance"`
}

type atomVenueDetailsResponse struct {
	VenueDetails []atomVenueDetails `json:"venueDetails"`
	PageInfo     atomPageInfo       `json:"pageInfo"`
}

type atomLocalDateBounds struct {
	Start string `json:"localStartDate"`
	End   string `json:"localEndDate"`
}

type atomShowtimesForVenuesRequest struct {
	VenueIDs                 []string            `json:"venueIds"`
	LocalDateBounds          atomLocalDateBounds `json:"localDateBounds"`
	IncludeProductionDetails bool                `json:"includeProductionDetails"`
}

type atomPrice struct {
	Value        float64 `json:"value"`
	CurrencyCode string  `json:"currencyCode"`
}

type atomOffer struct {
	Label string    `json:"label"`
	Price atomPrice `json:"price"`
}

type atomOfferData struct {
	Offers []atomOffer `json:"offers"`
}

type atomShowtimeDetail struct {
	ShowtimeID         string        `json:"showtimeId"`
	ProductionID       string        `json:"productionId"`
	ProductionTitle    string        `json:"productionTitle"`
	VenueID            string        `json:"venueId"`
	OfferData          atomOfferData `json:"offerData"`
	UTCShowtimeStart   string        `json:"utcShowtimeStart"`
	LocalShowtimeStart string        `json:"localShowtimeStart"`
	Attributes         []string      `json:"attributes"`
	CheckoutURL        string        `json:"checkoutUrl"`
}

type atomVenueShowtimeDetails struct {
	ShowtimeDetails []atomShowtimeDetail `json:"showtimeDetails"`
}

type atomAttribute struct {
	FriendlyName string `json:"friendlyName"`
	Description  string `json:"description"`
}

type atomProduction struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type atomShowtimesForVenuesResponse struct {
	VenueShowtimeDetailsMap map[string]atomVenueShowtimeDetails `json:"venueShowtimeDetailsMap"`
	AttributeMap            map[string]atomAttribute            `json:"attributeMap"`
	ProductionDetailsMap    map[string]atomProduction           `json:"productionDetailsMap"`
}

type atomSeat struct {
	SeatID   string `json:"seatId"`
	Row      string `json:"row"`
	Number   string `json:"number"`
	Status   string `json:"status"`
	SeatType string `json:"seatType"`
}

type atomAuditoriumDiscoveryResponse struct {
	AuditoriumID     string     `json:"auditoriumId"`
	ShowtimeID       string     `json:"showtimeId"`
	SeatMapAvailable bool       `json:"seatMapAvailable"`
	Seats            []atomSeat `json:"seats"`
}

func normalizeAtomShowtimes(payload atomShowtimesForVenuesResponse, venues map[string]atomVenue, query domain.QueryRequest) []domain.Showtime {
	result := []domain.Showtime{}
	for venueID, group := range payload.VenueShowtimeDetailsMap {
		venue, ok := venues[venueID]
		if !ok {
			continue
		}
		for _, item := range group.ShowtimeDetails {
			title := item.ProductionTitle
			if production, found := payload.ProductionDetailsMap[item.ProductionID]; found && production.Name != "" {
				title = production.Name
			}
			if !titleMatches(query.MovieQuery, title) {
				continue
			}
			startsAt, err := parseAtomTime(item, query.Time.Timezone)
			if err != nil {
				continue
			}
			attributeNames := make([]string, 0, len(item.Attributes)*2)
			for _, id := range item.Attributes {
				attributeNames = append(attributeNames, id)
				if attribute, found := payload.AttributeMap[id]; found {
					attributeNames = append(attributeNames, attribute.FriendlyName, attribute.Description)
				}
			}
			format, amenities, captions, audio := classifyAttributes(attributeNames, false)
			reserved := containsFold(item.Attributes, "RESERVED") || contains(amenities, "reserved_seating")
			if !reserved {
				continue
			}
			price, currency := preferredAtomPrice(item.OfferData.Offers, query.TicketCount)
			result = append(result, domain.Showtime{
				ID: item.ShowtimeID, MovieTitle: title, VenueName: venue.Name, StartsAt: startsAt,
				Format: format, DistanceMiles: venue.DistanceMiles, TotalPrice: price, Currency: currency,
				Amenities: amenities, Captions: captions, AudioDescription: audio, ReservedSeating: true,
				BookingURL: validAtomCheckoutURL(item.CheckoutURL), InventoryProvider: "atom-tickets-partner-api", SiteID: venue.ID,
			})
		}
	}
	return result
}

func parseAtomTime(showtime atomShowtimeDetail, timezone string) (time.Time, error) {
	for _, value := range []string{showtime.LocalShowtimeStart, showtime.UTCShowtimeStart} {
		if parsed, err := time.Parse(time.RFC3339, value); err == nil {
			return parsed, nil
		}
	}
	if showtime.LocalShowtimeStart != "" {
		location := time.UTC
		if timezone != "" {
			if parsed, err := time.LoadLocation(timezone); err == nil {
				location = parsed
			}
		}
		return time.ParseInLocation("2006-01-02T15:04:05", showtime.LocalShowtimeStart, location)
	}
	return time.Time{}, errors.New("Atom showtime omitted a valid start time")
}

func preferredAtomPrice(offers []atomOffer, count int) (*float64, string) {
	if count < 1 {
		count = 1
	}
	for _, preference := range []string{"adult", "ticket"} {
		for _, offer := range offers {
			if offer.Price.Value > 0 && strings.Contains(strings.ToLower(offer.Label), preference) {
				total := offer.Price.Value * float64(count)
				return &total, offer.Price.CurrencyCode
			}
		}
	}
	if len(offers) == 1 && offers[0].Price.Value > 0 {
		total := offers[0].Price.Value * float64(count)
		return &total, offers[0].Price.CurrencyCode
	}
	return nil, ""
}

func validAtomCheckoutURL(value string) string {
	parsed, err := url.Parse(strings.TrimSpace(value))
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" {
		return ""
	}
	host := strings.ToLower(parsed.Hostname())
	if host != "atomtickets.com" && !strings.HasSuffix(host, ".atomtickets.com") {
		return ""
	}
	return parsed.String()
}

func normalizeAtomSeats(source []atomSeat) []domain.Seat {
	type rowGroup struct {
		label string
		seats []atomSeat
	}
	groups := []rowGroup{}
	rowIndex := map[string]int{}
	for _, seat := range source {
		if seat.SeatID == "" {
			continue
		}
		seat.Row = strings.TrimSpace(seat.Row)
		seat.Number = strings.TrimSpace(seat.Number)
		if seat.Row == "" {
			seat.Row = leadingLetters(seat.Number)
		}
		if seat.Row == "" {
			seat.Row = "?"
		}
		index, exists := rowIndex[seat.Row]
		if !exists {
			index = len(groups)
			rowIndex[seat.Row] = index
			groups = append(groups, rowGroup{label: seat.Row})
		}
		groups[index].seats = append(groups[index].seats, seat)
	}
	result := make([]domain.Seat, 0, len(source))
	rowDenominator := math.Max(1, float64(len(groups)-1))
	for rowPosition, group := range groups {
		sort.SliceStable(group.seats, func(i, j int) bool {
			left, leftOK := seatNumber(group.seats[i].Number)
			right, rightOK := seatNumber(group.seats[j].Number)
			if leftOK && rightOK {
				return left < right
			}
			return group.seats[i].Number < group.seats[j].Number
		})
		numbers := make([]int, len(group.seats))
		allNumeric := true
		minNumber, maxNumber := math.MaxInt, math.MinInt
		for index, seat := range group.seats {
			number, ok := seatNumber(seat.Number)
			if !ok {
				allNumeric = false
				break
			}
			numbers[index] = number
			if number < minNumber {
				minNumber = number
			}
			if number > maxNumber {
				maxNumber = number
			}
		}
		for column, seat := range group.seats {
			x := float64(column) / math.Max(1, float64(len(group.seats)-1))
			index := column
			if allNumeric {
				index = numbers[column]
				if maxNumber > minNumber {
					x = float64(numbers[column]-minNumber) / float64(maxNumber-minNumber)
				}
			}
			label := seat.Number
			if label == "" {
				label = strconv.Itoa(column + 1)
			}
			if !strings.HasPrefix(strings.ToUpper(label), strings.ToUpper(group.label)) {
				label = group.label + label
			}
			result = append(result, domain.Seat{
				ID: seat.SeatID, Label: label, Row: group.label, Index: index,
				X: x, Y: float64(rowPosition) / rowDenominator,
				Type: normalizeAtomSeatType(seat.SeatType), Status: normalizeAtomStatus(seat.Status),
			})
		}
	}
	return result
}

func normalizeAtomSeatType(value string) string {
	normalized := strings.ToLower(value)
	switch {
	case strings.Contains(normalized, "wheel"):
		return "wheelchair"
	case strings.Contains(normalized, "companion"):
		return "companion"
	case strings.Contains(normalized, "reclin"), strings.Contains(normalized, "premium"):
		return "recliner"
	case strings.Contains(normalized, "sofa"), strings.Contains(normalized, "loveseat"):
		return "sofa"
	default:
		return "standard"
	}
}

func normalizeAtomStatus(value string) string {
	switch strings.ToUpper(strings.TrimSpace(value)) {
	case "AVAILABLE":
		return "available"
	case "OCCUPIED", "SOLD":
		return "sold"
	case "HELD":
		return "held"
	case "BROKEN":
		return "broken"
	default:
		return "house"
	}
}

func seatNumber(value string) (int, bool) {
	digits := ""
	for _, character := range value {
		if character >= '0' && character <= '9' {
			digits += string(character)
		}
	}
	if digits == "" {
		return 0, false
	}
	number, err := strconv.Atoi(digits)
	return number, err == nil
}

func leadingLetters(value string) string {
	var builder strings.Builder
	for _, character := range value {
		if (character >= 'A' && character <= 'Z') || (character >= 'a' && character <= 'z') {
			builder.WriteRune(character)
			continue
		}
		break
	}
	return builder.String()
}

func cloneValues(source url.Values) url.Values {
	copy := url.Values{}
	for key, values := range source {
		copy[key] = append([]string(nil), values...)
	}
	return copy
}

func containsFold(values []string, target string) bool {
	for _, value := range values {
		if strings.EqualFold(value, target) {
			return true
		}
	}
	return false
}

func contains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func titleMatches(query, title string) bool {
	query = normalizeText(query)
	title = normalizeText(title)
	return query != "" && title != "" && (query == title || strings.Contains(title, query) || strings.Contains(query, title))
}

func normalizeText(value string) string {
	var builder strings.Builder
	space := false
	for _, character := range strings.ToLower(value) {
		if (character >= 'a' && character <= 'z') || (character >= '0' && character <= '9') {
			builder.WriteRune(character)
			space = false
		} else if !space {
			builder.WriteByte(' ')
			space = true
		}
	}
	return strings.TrimSpace(builder.String())
}

func haversineMiles(lat1, lon1, lat2, lon2 float64) float64 {
	const earthRadiusMiles = 3958.7613
	toRadians := math.Pi / 180
	deltaLatitude := (lat2 - lat1) * toRadians
	deltaLongitude := (lon2 - lon1) * toRadians
	a := math.Sin(deltaLatitude/2)*math.Sin(deltaLatitude/2) +
		math.Cos(lat1*toRadians)*math.Cos(lat2*toRadians)*math.Sin(deltaLongitude/2)*math.Sin(deltaLongitude/2)
	return earthRadiusMiles * 2 * math.Atan2(math.Sqrt(a), math.Sqrt(1-a))
}

func classifyAttributes(values []string, requires3D bool) (string, []string, string, bool) {
	joined := strings.ToLower(strings.Join(values, " "))
	joined = strings.NewReplacer("_", " ", "-", " ").Replace(joined)
	format := "standard"
	for _, candidate := range []string{"imax", "dolby", "screenx", "rpdx", "xd"} {
		if strings.Contains(joined, candidate) {
			format = candidate
			break
		}
	}
	if requires3D || strings.Contains(joined, "reald3d") || strings.Contains(joined, "real d 3d") {
		format = "3d"
	}
	amenities := []string{}
	if strings.Contains(joined, "reserved") {
		amenities = append(amenities, "reserved_seating")
	}
	for keyword, normalized := range map[string]string{
		"recliner": "recliner", "laser": "laser_projection", "dine": "dine_in", "alcohol": "alcohol",
	} {
		if strings.Contains(joined, keyword) {
			amenities = append(amenities, normalized)
		}
	}
	captions := "none"
	if strings.Contains(joined, "open caption") {
		captions = "open"
	} else if strings.Contains(joined, "closed caption") || strings.Contains(joined, " cc ") || strings.HasPrefix(joined, "cc ") {
		captions = "closed"
	}
	audio := strings.Contains(joined, "audio description") || strings.Contains(joined, "audio desc") || strings.Contains(joined, "descriptive audio") || strings.Contains(joined, "dvs")
	return format, amenities, captions, audio
}
