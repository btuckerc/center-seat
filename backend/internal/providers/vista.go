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
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"centerseat/backend/internal/domain"
)

type VistaConfig struct {
	APIBaseURL         string
	AuthURL            string
	ClientID           string
	Username           string
	Password           string
	RegionCode         string
	SiteIDs            []string
	BookingURLTemplate string
	RequestTimeout     time.Duration
}

type Vista struct {
	config VistaConfig
	client *http.Client

	tokenMu     sync.Mutex
	token       string
	tokenExpiry time.Time

	layoutMu    sync.RWMutex
	layoutCache map[string]vistaSeatLayout

	statusMu    sync.RWMutex
	lastSuccess time.Time
}

func NewVista(config VistaConfig, client *http.Client) (*Vista, error) {
	config.APIBaseURL = strings.TrimRight(strings.TrimSpace(config.APIBaseURL), "/")
	config.AuthURL = strings.TrimSpace(config.AuthURL)
	config.ClientID = strings.TrimSpace(config.ClientID)
	config.Username = strings.TrimSpace(config.Username)
	config.RegionCode = strings.TrimSpace(config.RegionCode)
	missing := []string{}
	for name, value := range map[string]string{
		"VISTA_API_BASE_URL": config.APIBaseURL,
		"VISTA_AUTH_URL":     config.AuthURL,
		"VISTA_CLIENT_ID":    config.ClientID,
		"VISTA_USERNAME":     config.Username,
		"VISTA_PASSWORD":     config.Password,
	} {
		if strings.TrimSpace(value) == "" {
			missing = append(missing, name)
		}
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		return nil, fmt.Errorf("missing required Vista configuration: %s", strings.Join(missing, ", "))
	}
	for _, raw := range []string{config.APIBaseURL, config.AuthURL} {
		parsed, err := url.Parse(raw)
		if err != nil || parsed.Scheme != "https" || parsed.Host == "" {
			return nil, fmt.Errorf("Vista URLs must be absolute HTTPS URLs: %q", raw)
		}
	}
	if config.RequestTimeout <= 0 {
		config.RequestTimeout = 8 * time.Second
	}
	if client == nil {
		client = &http.Client{Timeout: config.RequestTimeout}
	}
	return &Vista{config: config, client: client, layoutCache: map[string]vistaSeatLayout{}}, nil
}

func (v *Vista) Name() string { return "vista-digital-platform" }

func (v *Vista) ProviderStatus(kind string) domain.ProviderStatus {
	v.statusMu.RLock()
	last := v.lastSuccess
	v.statusMu.RUnlock()
	status := domain.ProviderStatus{
		Name: v.Name(), Kind: kind, Status: "healthy", Configured: true,
		Coverage: "licensed Vista OCAPI tenant", Message: "Licensed provider configured",
	}
	if last.IsZero() {
		status.Status = "degraded"
		status.Message = "Configured; awaiting first successful provider request"
	} else {
		status.LastSuccessAt = &last
	}
	return status
}

func (v *Vista) markSuccess() {
	now := time.Now().UTC()
	v.statusMu.Lock()
	v.lastSuccess = now
	v.statusMu.Unlock()
}

func (v *Vista) Discover(ctx context.Context, query domain.QueryRequest) ([]domain.Showtime, error) {
	start, err := time.Parse(time.DateOnly, query.Dates.Start)
	if err != nil {
		return nil, err
	}
	end, err := time.Parse(time.DateOnly, query.Dates.End)
	if err != nil {
		return nil, err
	}
	type result struct {
		payload vistaShowtimeList
		err     error
	}
	days := int(end.Sub(start).Hours()/24) + 1
	results := make(chan result, days)
	sem := make(chan struct{}, 5)
	var wg sync.WaitGroup
	for day := start; !day.After(end); day = day.AddDate(0, 0, 1) {
		date := day.Format(time.DateOnly)
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
			payload, err := v.showtimesForDate(ctx, date)
			results <- result{payload: payload, err: err}
		}()
	}
	wg.Wait()
	close(results)

	all := make([]domain.Showtime, 0, days*20)
	for item := range results {
		if item.err != nil {
			return nil, item.err
		}
		all = append(all, v.normalizeShowtimes(item.payload, query)...)
	}
	sort.Slice(all, func(i, j int) bool { return all[i].StartsAt.Before(all[j].StartsAt) })
	v.markSuccess()
	return all, nil
}

func (v *Vista) showtimesForDate(ctx context.Context, date string) (vistaShowtimeList, error) {
	query := url.Values{}
	for _, siteID := range v.config.SiteIDs {
		query.Add("siteIds", siteID)
	}
	var payload vistaShowtimeList
	err := v.getJSON(ctx, "/ocapi/v1/showtimes/by-business-date/"+url.PathEscape(date), query, &payload)
	return payload, err
}

func (v *Vista) normalizeShowtimes(payload vistaShowtimeList, query domain.QueryRequest) []domain.Showtime {
	films := map[string]vistaFilm{}
	for _, film := range payload.RelatedData.Films {
		films[film.ID] = film
	}
	sites := map[string]vistaSite{}
	for _, site := range payload.RelatedData.Sites {
		sites[site.ID] = site
	}
	screens := map[string]vistaScreen{}
	for _, screen := range payload.RelatedData.Screens {
		screens[screen.ID] = screen
	}
	attributes := map[string]string{}
	for _, attribute := range payload.RelatedData.Attributes {
		attributes[attribute.ID] = attribute.ShortName.Text
	}

	result := make([]domain.Showtime, 0, len(payload.Showtimes))
	for _, showtime := range payload.Showtimes {
		film, filmOK := films[showtime.FilmID]
		site, siteOK := sites[showtime.SiteID]
		if !filmOK || !siteOK || showtime.IsSoldOut || !showtime.IsAllocatedSeating || showtime.SeatLayoutID == "" {
			continue
		}
		if !titleMatches(query.MovieQuery, film.Title.Text) {
			continue
		}
		distance, locationOK := siteDistance(site, query.Location)
		if !locationOK || distance > query.Location.RadiusMiles {
			continue
		}
		startsAt, err := time.Parse(time.RFC3339, showtime.Schedule.StartsAt)
		if err != nil {
			continue
		}
		attributeNames := make([]string, 0, len(showtime.AttributeIDs))
		for _, id := range showtime.AttributeIDs {
			if name := attributes[id]; name != "" {
				attributeNames = append(attributeNames, name)
			}
		}
		format, amenities, captions, audio := classifyAttributes(attributeNames, showtime.Requires3DGlasses)
		screen := screens[showtime.ScreenID]
		bookingURL := renderBookingURL(v.config.BookingURLTemplate, showtime.ID, showtime.SiteID)
		result = append(result, domain.Showtime{
			ID: showtime.ID, MovieTitle: film.Title.Text, VenueName: site.Name.Text,
			AuditoriumName: screen.Name.Text, StartsAt: startsAt, Format: format,
			DistanceMiles: distance, Amenities: amenities, Captions: captions,
			AudioDescription: audio, ReservedSeating: true, BookingURL: bookingURL,
			InventoryProvider: v.Name(), SeatLayoutID: showtime.SeatLayoutID,
			ScreenID: showtime.ScreenID, SiteID: showtime.SiteID,
		})
	}
	return result
}

func (v *Vista) Supports(showtime domain.Showtime) bool {
	return showtime.InventoryProvider == v.Name() && showtime.SeatLayoutID != ""
}

func (v *Vista) GetAvailability(ctx context.Context, showtime domain.Showtime, final bool) (domain.Inventory, error) {
	if !v.Supports(showtime) {
		return domain.Inventory{}, errors.New("showtime is not backed by Vista allocated seating")
	}
	layout, err := v.getLayout(ctx, showtime.SeatLayoutID)
	if err != nil {
		return domain.Inventory{}, err
	}
	query := url.Values{}
	if !final {
		query.Set("preview", "true")
	}
	var availability vistaSeatAvailability
	if err := v.getJSON(ctx, "/ocapi/v1/showtimes/"+url.PathEscape(showtime.ID)+"/seat-availability", query, &availability); err != nil {
		return domain.Inventory{}, err
	}
	statuses := map[string]string{}
	for _, seat := range availability.SeatAvailabilities {
		statuses[seat.SeatID] = normalizeVistaStatus(seat.Status)
	}
	seats := normalizeVistaLayout(layout, statuses)
	if len(seats) == 0 {
		return domain.Inventory{}, errors.New("Vista returned an empty allocated seat layout")
	}
	v.markSuccess()
	freshFor := 12 * time.Second
	if final {
		freshFor = 5 * time.Second
	}
	return domain.Inventory{
		ShowtimeID: showtime.ID, Seats: seats, Confidence: "exact_coordinates",
		ObservedAt: time.Now().UTC(), FreshFor: freshFor,
	}, nil
}

func (v *Vista) getLayout(ctx context.Context, layoutID string) (vistaSeatLayout, error) {
	v.layoutMu.RLock()
	layout, ok := v.layoutCache[layoutID]
	v.layoutMu.RUnlock()
	if ok {
		return layout, nil
	}
	var payload struct {
		SeatLayout vistaSeatLayout `json:"seatLayout"`
	}
	if err := v.getJSON(ctx, "/ocapi/v1/seat-layouts/"+url.PathEscape(layoutID), nil, &payload); err != nil {
		return vistaSeatLayout{}, err
	}
	if payload.SeatLayout.ID == "" {
		return vistaSeatLayout{}, errors.New("Vista seat layout response is missing an ID")
	}
	v.layoutMu.Lock()
	v.layoutCache[layoutID] = payload.SeatLayout
	v.layoutMu.Unlock()
	return payload.SeatLayout, nil
}

func (v *Vista) getJSON(ctx context.Context, path string, query url.Values, destination any) error {
	for attempt := 0; attempt < 2; attempt++ {
		token, err := v.accessToken(ctx)
		if err != nil {
			return err
		}
		endpoint := v.config.APIBaseURL + path
		if len(query) > 0 {
			endpoint += "?" + query.Encode()
		}
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
		if err != nil {
			return err
		}
		request.Header.Set("Accept", "application/json")
		request.Header.Set("Authorization", "Bearer "+token)
		if v.config.RegionCode != "" {
			request.Header.Set("Connect-Region-Code", v.config.RegionCode)
		}
		response, err := v.client.Do(request)
		if err != nil {
			return fmt.Errorf("Vista request failed: %w", err)
		}
		body, readErr := io.ReadAll(io.LimitReader(response.Body, 16<<20))
		_ = response.Body.Close()
		if readErr != nil {
			return readErr
		}
		if response.StatusCode == http.StatusUnauthorized && attempt == 0 {
			v.invalidateToken()
			continue
		}
		if response.StatusCode < 200 || response.StatusCode >= 300 {
			return fmt.Errorf("Vista returned HTTP %d", response.StatusCode)
		}
		if len(body) == 0 {
			return nil
		}
		if err := json.Unmarshal(body, destination); err != nil {
			return fmt.Errorf("decode Vista response: %w", err)
		}
		return nil
	}
	return errors.New("Vista authentication failed after token refresh")
}

func (v *Vista) accessToken(ctx context.Context) (string, error) {
	v.tokenMu.Lock()
	defer v.tokenMu.Unlock()
	if v.token != "" && time.Until(v.tokenExpiry) > 2*time.Minute {
		return v.token, nil
	}
	form := url.Values{
		"grant_type": {"password"}, "client_id": {v.config.ClientID},
		"username": {v.config.Username}, "password": {v.config.Password},
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, v.config.AuthURL, strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("Accept", "application/json")
	response, err := v.client.Do(request)
	if err != nil {
		return "", fmt.Errorf("Vista authentication failed: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 1<<20))
		return "", fmt.Errorf("Vista authentication returned HTTP %d", response.StatusCode)
	}
	var payload struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&payload); err != nil {
		return "", err
	}
	if payload.AccessToken == "" {
		return "", errors.New("Vista authentication response omitted access_token")
	}
	if payload.ExpiresIn <= 0 {
		payload.ExpiresIn = 12 * 60 * 60
	}
	v.token = payload.AccessToken
	v.tokenExpiry = time.Now().Add(time.Duration(payload.ExpiresIn) * time.Second)
	return v.token, nil
}

func (v *Vista) invalidateToken() {
	v.tokenMu.Lock()
	v.token = ""
	v.tokenExpiry = time.Time{}
	v.tokenMu.Unlock()
}

type vistaText struct {
	Text string `json:"text"`
}
type vistaLocation struct {
	Latitude  float64 `json:"latitude"`
	Longitude float64 `json:"longitude"`
}
type vistaAddress struct {
	Line1              string `json:"line1"`
	Line2              string `json:"line2"`
	City               string `json:"city"`
	AdministrativeArea string `json:"administrativeArea"`
	PostalCode         string `json:"postalCode"`
}
type vistaContactDetails struct {
	Address vistaAddress `json:"address"`
}
type vistaSite struct {
	ID             string              `json:"id"`
	Name           vistaText           `json:"name"`
	Location       vistaLocation       `json:"location"`
	ContactDetails vistaContactDetails `json:"contactDetails"`
}
type vistaFilm struct {
	ID    string    `json:"id"`
	Title vistaText `json:"title"`
}
type vistaScreen struct {
	ID   string    `json:"id"`
	Name vistaText `json:"name"`
}
type vistaAttribute struct {
	ID        string    `json:"id"`
	ShortName vistaText `json:"shortName"`
}
type vistaSchedule struct {
	StartsAt string `json:"startsAt"`
}
type vistaShowtime struct {
	ID                 string        `json:"id"`
	Schedule           vistaSchedule `json:"schedule"`
	IsSoldOut          bool          `json:"isSoldOut"`
	SeatLayoutID       string        `json:"seatLayoutId"`
	FilmID             string        `json:"filmId"`
	SiteID             string        `json:"siteId"`
	ScreenID           string        `json:"screenId"`
	AttributeIDs       []string      `json:"attributeIds"`
	IsAllocatedSeating bool          `json:"isAllocatedSeating"`
	Requires3DGlasses  bool          `json:"requires3dGlasses"`
}
type vistaRelatedData struct {
	Sites      []vistaSite      `json:"sites"`
	Films      []vistaFilm      `json:"films"`
	Screens    []vistaScreen    `json:"screens"`
	Attributes []vistaAttribute `json:"attributes"`
}
type vistaShowtimeList struct {
	Showtimes   []vistaShowtime  `json:"showtimes"`
	RelatedData vistaRelatedData `json:"relatedData"`
}
type vistaBoundary struct {
	Left   float64 `json:"left"`
	Top    float64 `json:"top"`
	Right  float64 `json:"right"`
	Bottom float64 `json:"bottom"`
}
type vistaSeatPosition struct {
	AreaNumber   int `json:"areaNumber"`
	ColumnNumber int `json:"columnNumber"`
	RowNumber    int `json:"rowNumber"`
}
type vistaSeat struct {
	ID       string            `json:"id"`
	Label    string            `json:"label"`
	RowLabel string            `json:"rowLabel"`
	Type     string            `json:"type"`
	Position vistaSeatPosition `json:"position"`
}
type vistaRow struct {
	Number int         `json:"number"`
	Label  string      `json:"label"`
	Seats  []vistaSeat `json:"seats"`
}
type vistaArea struct {
	Boundary vistaBoundary `json:"boundary"`
	Rows     []vistaRow    `json:"rows"`
}
type vistaSeatLayout struct {
	ID       string        `json:"id"`
	Boundary vistaBoundary `json:"boundary"`
	Areas    []vistaArea   `json:"areas"`
}
type vistaSeatState struct {
	SeatID string `json:"seatId"`
	Status string `json:"status"`
}
type vistaSeatAvailability struct {
	SeatAvailabilities []vistaSeatState `json:"seatAvailabilities"`
	IsSoldOut          bool             `json:"isSoldOut"`
}

func normalizeVistaLayout(layout vistaSeatLayout, statuses map[string]string) []domain.Seat {
	type positioned struct {
		seat vistaSeat
		x, y float64
	}
	items := []positioned{}
	minX, maxX, minY, maxY := math.MaxFloat64, -math.MaxFloat64, math.MaxFloat64, -math.MaxFloat64
	for _, area := range layout.Areas {
		for _, row := range area.Rows {
			for _, seat := range row.Seats {
				x := area.Boundary.Left + float64(seat.Position.ColumnNumber)
				y := area.Boundary.Top + float64(seat.Position.RowNumber)
				items = append(items, positioned{seat: seat, x: x, y: y})
				minX, maxX = math.Min(minX, x), math.Max(maxX, x)
				minY, maxY = math.Min(minY, y), math.Max(maxY, y)
			}
		}
	}
	spanX, spanY := maxX-minX, maxY-minY
	if spanX <= 0 {
		spanX = 1
	}
	if spanY <= 0 {
		spanY = 1
	}
	result := make([]domain.Seat, 0, len(items))
	for _, item := range items {
		status := statuses[item.seat.ID]
		if status == "" {
			status = "house"
		}
		row := item.seat.RowLabel
		if row == "" {
			row = "?"
		}
		label := item.seat.Label
		if label == "" {
			label = strconv.Itoa(item.seat.Position.ColumnNumber)
		}
		result = append(result, domain.Seat{
			ID: item.seat.ID, Label: row + label, Row: row, Index: item.seat.Position.ColumnNumber,
			X: (item.x - minX) / spanX, Y: (item.y - minY) / spanY,
			Type: normalizeVistaSeatType(item.seat.Type), Status: status,
		})
	}
	return result
}

func normalizeVistaSeatType(value string) string {
	switch strings.ToLower(value) {
	case "wheelchair":
		return "wheelchair"
	case "companion":
		return "companion"
	case "sofaleft", "sofamiddle", "sofaright":
		return "sofa"
	default:
		return "standard"
	}
}

func normalizeVistaStatus(value string) string {
	switch strings.ToLower(value) {
	case "available":
		return "available"
	case "sold":
		return "sold"
	case "broken":
		return "broken"
	case "house":
		return "house"
	default:
		return "house"
	}
}

func titleMatches(query, title string) bool {
	q, t := normalizeText(query), normalizeText(title)
	return q != "" && t != "" && (q == t || strings.Contains(t, q) || strings.Contains(q, t))
}

func normalizeText(value string) string {
	var builder strings.Builder
	space := false
	for _, r := range strings.ToLower(value) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			builder.WriteRune(r)
			space = false
		} else if !space {
			builder.WriteByte(' ')
			space = true
		}
	}
	return strings.TrimSpace(builder.String())
}

func siteDistance(site vistaSite, location domain.LocationConstraint) (float64, bool) {
	if location.Latitude != 0 || location.Longitude != 0 {
		if site.Location.Latitude == 0 && site.Location.Longitude == 0 {
			return 0, false
		}
		return haversineMiles(location.Latitude, location.Longitude, site.Location.Latitude, site.Location.Longitude), true
	}
	query := normalizeText(location.Query)
	if query == "" {
		return 0, false
	}
	haystack := normalizeText(strings.Join([]string{
		site.Name.Text, site.ContactDetails.Address.Line1, site.ContactDetails.Address.Line2,
		site.ContactDetails.Address.City, site.ContactDetails.Address.AdministrativeArea, site.ContactDetails.Address.PostalCode,
	}, " "))
	for _, token := range strings.Fields(query) {
		if len(token) > 1 && !strings.Contains(haystack, token) {
			return 0, false
		}
	}
	return 0, true
}

func haversineMiles(lat1, lon1, lat2, lon2 float64) float64 {
	const radius = 3958.7613
	toRad := math.Pi / 180
	dLat, dLon := (lat2-lat1)*toRad, (lon2-lon1)*toRad
	a := math.Sin(dLat/2)*math.Sin(dLat/2) + math.Cos(lat1*toRad)*math.Cos(lat2*toRad)*math.Sin(dLon/2)*math.Sin(dLon/2)
	return radius * 2 * math.Atan2(math.Sqrt(a), math.Sqrt(1-a))
}

func classifyAttributes(values []string, requires3D bool) (string, []string, string, bool) {
	joined := strings.ToLower(strings.Join(values, " "))
	format := "standard"
	for _, candidate := range []string{"imax", "dolby", "screenx", "rpdx", "xd"} {
		if strings.Contains(joined, candidate) {
			format = candidate
			break
		}
	}
	if requires3D {
		format = "3d"
	}
	amenities := []string{"reserved_seating"}
	for keyword, normalized := range map[string]string{"recliner": "recliner", "laser": "laser_projection", "dine": "dine_in", "alcohol": "alcohol"} {
		if strings.Contains(joined, keyword) {
			amenities = append(amenities, normalized)
		}
	}
	captions := "none"
	if strings.Contains(joined, "open caption") {
		captions = "open"
	} else if strings.Contains(joined, "closed caption") {
		captions = "closed"
	}
	audio := strings.Contains(joined, "audio description") || strings.Contains(joined, "descriptive audio")
	return format, amenities, captions, audio
}

func renderBookingURL(template, showtimeID, siteID string) string {
	if strings.TrimSpace(template) == "" {
		return ""
	}
	result := strings.ReplaceAll(template, "{showtimeId}", url.PathEscape(showtimeID))
	return strings.ReplaceAll(result, "{siteId}", url.PathEscape(siteID))
}
