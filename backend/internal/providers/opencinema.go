package providers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"centerseat/backend/internal/domain"
)

const openCinemaMaximumPages = 20

type OpenCinemaConfig struct {
	BaseURL        string
	APIKey         string
	RequestTimeout time.Duration
}

type OpenCinema struct {
	config OpenCinemaConfig
	client *http.Client

	statusMu    sync.RWMutex
	lastSuccess time.Time
}

func NewOpenCinema(config OpenCinemaConfig, client *http.Client) (*OpenCinema, error) {
	config.BaseURL = strings.TrimRight(strings.TrimSpace(config.BaseURL), "/")
	config.APIKey = strings.TrimSpace(config.APIKey)
	if config.BaseURL == "" {
		config.BaseURL = "https://opencinema.app"
	}
	if config.APIKey == "" {
		return nil, errors.New("missing required Open Cinema configuration: OPEN_CINEMA_API_KEY")
	}
	parsed, err := url.Parse(config.BaseURL)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" {
		return nil, fmt.Errorf("OPEN_CINEMA_API_BASE_URL must be an absolute HTTPS URL: %q", config.BaseURL)
	}
	if config.RequestTimeout <= 0 {
		config.RequestTimeout = 8 * time.Second
	}
	if client == nil {
		client = &http.Client{Timeout: config.RequestTimeout}
	}
	return &OpenCinema{config: config, client: client}, nil
}

func (o *OpenCinema) Name() string { return "open-cinema-project" }

func (o *OpenCinema) ProviderStatus(kind string) domain.ProviderStatus {
	o.statusMu.RLock()
	last := o.lastSuccess
	o.statusMu.RUnlock()
	status := domain.ProviderStatus{
		Name: o.Name(), Kind: kind, Status: "healthy", Configured: true,
		Coverage:     "Independent, repertory, and arthouse cinemas",
		LocationMode: "coordinates",
		Message:      "Self-service Open Cinema API key configured",
	}
	if last.IsZero() {
		status.Status = "degraded"
		status.Message = "Configured; awaiting first successful Open Cinema request"
	} else {
		status.LastSuccessAt = &last
	}
	return status
}

func (o *OpenCinema) markSuccess() {
	now := time.Now().UTC()
	o.statusMu.Lock()
	o.lastSuccess = now
	o.statusMu.Unlock()
}

func (o *OpenCinema) Check(ctx context.Context) error {
	var payload map[string]any
	if err := o.doJSON(ctx, "/api/v1/public/status", nil, &payload); err != nil {
		return err
	}
	o.markSuccess()
	return nil
}

func (o *OpenCinema) Discover(ctx context.Context, query domain.QueryRequest) ([]domain.Showtime, error) {
	if query.Location.Latitude == 0 && query.Location.Longitude == 0 {
		return nil, errors.New("Open Cinema discovery requires latitude and longitude; use precise location")
	}
	parameters := url.Values{
		"lat":       {strconv.FormatFloat(query.Location.Latitude, 'f', 6, 64)},
		"lon":       {strconv.FormatFloat(query.Location.Longitude, 'f', 6, 64)},
		"radius_km": {strconv.FormatFloat(query.Location.RadiusMiles*1.609344, 'f', 3, 64)},
		"title":     {query.MovieQuery},
		"limit":     {"200"},
	}
	all := make([]openCinemaScreening, 0, 200)
	for page := 0; page < openCinemaMaximumPages; page++ {
		var response openCinemaScreeningsResponse
		if err := o.doJSON(ctx, "/api/v1/public/screenings", parameters, &response); err != nil {
			return nil, err
		}
		all = append(all, response.Screenings...)
		if !response.Pagination.HasMore {
			break
		}
		if strings.TrimSpace(response.Pagination.NextCursor) == "" {
			return nil, errors.New("Open Cinema pagination reported more results without a cursor")
		}
		if page == openCinemaMaximumPages-1 {
			return nil, fmt.Errorf("Open Cinema pagination exceeded the safety limit of %d pages", openCinemaMaximumPages)
		}
		parameters.Set("cursor", response.Pagination.NextCursor)
	}
	showtimes := normalizeOpenCinemaScreenings(all, query)
	o.markSuccess()
	return showtimes, nil
}

func (o *OpenCinema) doJSON(ctx context.Context, path string, query url.Values, destination any) error {
	endpoint := o.config.BaseURL + path
	if len(query) > 0 {
		endpoint += "?" + query.Encode()
	}
	for attempt := 0; attempt < 3; attempt++ {
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
		if err != nil {
			return err
		}
		request.Header.Set("Accept", "application/json")
		request.Header.Set("Authorization", "Bearer "+o.config.APIKey)
		request.Header.Set("User-Agent", "CenterSeat/1.0")
		response, err := o.client.Do(request)
		if err != nil {
			return fmt.Errorf("Open Cinema request failed: %w", err)
		}
		body, readErr := io.ReadAll(io.LimitReader(response.Body, 12<<20))
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
			var problem openCinemaErrorResponse
			_ = json.Unmarshal(body, &problem)
			message := strings.TrimSpace(problem.Error.Message)
			if message == "" {
				message = http.StatusText(response.StatusCode)
			}
			return fmt.Errorf("Open Cinema returned HTTP %d: %s", response.StatusCode, message)
		}
		if destination == nil || len(body) == 0 {
			return nil
		}
		if err := json.Unmarshal(body, destination); err != nil {
			return fmt.Errorf("decode Open Cinema response: %w", err)
		}
		return nil
	}
	return errors.New("Open Cinema request exhausted retries")
}

type openCinemaScreeningsResponse struct {
	Screenings []openCinemaScreening `json:"screenings"`
	Pagination struct {
		HasMore    bool   `json:"has_more"`
		NextCursor string `json:"next_cursor"`
	} `json:"pagination"`
}

type openCinemaScreening struct {
	ID                    string   `json:"id"`
	FilmTitle             string   `json:"film_title"`
	TheaterID             string   `json:"theater_id"`
	TheaterName           string   `json:"theater_name"`
	TheaterTimezone       string   `json:"theater_timezone"`
	StartTime             string   `json:"start_time"`
	Formats               []string `json:"formats"`
	IsSoldOut             bool     `json:"is_sold_out"`
	DistanceKM            *float64 `json:"distance_km"`
	AccessibilityFeatures []string `json:"accessibility_features"`
	Checkout              struct {
		URL *string `json:"url"`
	} `json:"checkout"`
}

type openCinemaErrorResponse struct {
	Error struct {
		Message string `json:"message"`
	} `json:"error"`
}

func normalizeOpenCinemaScreenings(screenings []openCinemaScreening, query domain.QueryRequest) []domain.Showtime {
	result := make([]domain.Showtime, 0, len(screenings))
	for _, screening := range screenings {
		if screening.ID == "" || screening.IsSoldOut || !titleMatches(query.MovieQuery, screening.FilmTitle) {
			continue
		}
		startsAt, err := time.Parse(time.RFC3339Nano, screening.StartTime)
		if err != nil || !dateWithinQuery(startsAt, screening.TheaterTimezone, query.Dates) {
			continue
		}
		attributes := append([]string(nil), screening.Formats...)
		attributes = append(attributes, screening.AccessibilityFeatures...)
		format, amenities, captions, audio := classifyAttributes(attributes, false)
		distance := 0.0
		if screening.DistanceKM != nil {
			distance = *screening.DistanceKM / 1.609344
		}
		bookingURL := ""
		if screening.Checkout.URL != nil {
			bookingURL = validProviderCheckoutURL(*screening.Checkout.URL)
		}
		result = append(result, domain.Showtime{
			ID: screening.ID, MovieTitle: screening.FilmTitle, VenueName: screening.TheaterName,
			StartsAt: startsAt, Format: format, DistanceMiles: distance,
			Amenities: amenities, Captions: captions, AudioDescription: audio,
			ReservedSeating: false, BookingURL: bookingURL, SiteID: screening.TheaterID,
		})
	}
	return result
}

func dateWithinQuery(value time.Time, timezone string, dates domain.DateConstraint) bool {
	location := time.UTC
	if parsed, err := time.LoadLocation(timezone); err == nil {
		location = parsed
	}
	localDate := value.In(location).Format(time.DateOnly)
	return localDate >= dates.Start && localDate <= dates.End
}

func validProviderCheckoutURL(value string) string {
	parsed, err := url.Parse(strings.TrimSpace(value))
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil {
		return ""
	}
	host := strings.ToLower(parsed.Hostname())
	if host == "example.com" || strings.HasSuffix(host, ".example.com") || host == "localhost" {
		return ""
	}
	return parsed.String()
}
