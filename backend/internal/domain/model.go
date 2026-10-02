package domain

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

type LocationConstraint struct {
	Query       string  `json:"query,omitempty"`
	Latitude    float64 `json:"latitude,omitempty"`
	Longitude   float64 `json:"longitude,omitempty"`
	RadiusMiles float64 `json:"radius_miles"`
}

type DateConstraint struct {
	Start string `json:"start"`
	End   string `json:"end"`
}

type TimeConstraint struct {
	Mode     string `json:"mode,omitempty"`
	Start    string `json:"start,omitempty"`
	End      string `json:"end,omitempty"`
	Timezone string `json:"timezone,omitempty"`
}

type SeatZone struct {
	MinimumX float64 `json:"minimum_x"`
	MaximumX float64 `json:"maximum_x"`
	MinimumY float64 `json:"minimum_y"`
	MaximumY float64 `json:"maximum_y"`
}

type QueryRequest struct {
	MovieQuery                string             `json:"movie_query"`
	MovieID                   string             `json:"movie_id,omitempty"`
	Location                  LocationConstraint `json:"location"`
	Dates                     DateConstraint     `json:"dates"`
	Time                      TimeConstraint     `json:"time,omitempty"`
	TicketCount               int                `json:"ticket_count"`
	SeatProfile               string             `json:"seat_profile"`
	CustomSeatZone            *SeatZone          `json:"custom_seat_zone,omitempty"`
	Formats                   []string           `json:"formats,omitempty"`
	Language                  string             `json:"language,omitempty"`
	Captions                  string             `json:"captions,omitempty"`
	AudioDescription          bool               `json:"audio_description,omitempty"`
	WheelchairSpaces          int                `json:"wheelchair_spaces,omitempty"`
	CompanionSeats            int                `json:"companion_seats,omitempty"`
	AmenitiesRequired         []string           `json:"amenities_required,omitempty"`
	MaxDistanceMiles          float64            `json:"max_distance_miles,omitempty"`
	MaxTotalPrice             *float64           `json:"max_total_price,omitempty"`
	MinStartNoticeMinutes     int                `json:"min_start_notice_minutes,omitempty"`
	AllowSplitParty           bool               `json:"allow_split_party,omitempty"`
	AllowUnknownPrice         bool               `json:"allow_unknown_price,omitempty"`
	ExcludeFirstRows          int                `json:"exclude_first_rows,omitempty"`
	MinimumGeometryConfidence string             `json:"minimum_geometry_confidence,omitempty"`
	CandidateLimit            int                `json:"candidate_limit,omitempty"`
}

func (q *QueryRequest) SetDefaults() {
	q.MovieQuery = strings.TrimSpace(q.MovieQuery)
	q.MovieID = strings.TrimSpace(q.MovieID)
	q.Location.Query = strings.TrimSpace(q.Location.Query)
	if q.Location.RadiusMiles == 0 {
		q.Location.RadiusMiles = 25
	}
	if q.MaxDistanceMiles == 0 {
		q.MaxDistanceMiles = q.Location.RadiusMiles
	}
	if q.TicketCount == 0 {
		q.TicketCount = 1
	}
	if q.SeatProfile == "" {
		q.SeatProfile = "balanced"
	}
	if q.Time.Mode == "" {
		q.Time.Mode = "any"
	}
	if q.Captions == "" {
		q.Captions = "any"
	}
	if q.MinimumGeometryConfidence == "" {
		q.MinimumGeometryConfidence = "row_geometry"
	}
	if q.CandidateLimit == 0 {
		q.CandidateLimit = automaticCandidateLimit(q.Dates)
	}
}

func (q QueryRequest) Validate() error {
	if q.MovieQuery == "" {
		return errors.New("movie_query is required")
	}
	if q.Location.Latitude == 0 && q.Location.Longitude == 0 && q.Location.Query == "" {
		return errors.New("location requires latitude and longitude or a provider-supported location query")
	}
	if q.Location.Latitude < -90 || q.Location.Latitude > 90 || q.Location.Longitude < -180 || q.Location.Longitude > 180 {
		return errors.New("location coordinates are outside valid latitude/longitude bounds")
	}
	if q.Location.RadiusMiles <= 0 || q.Location.RadiusMiles > 49 {
		return errors.New("location.radius_miles must be between 0 and 49")
	}
	if len(q.MovieID) > 128 {
		return errors.New("movie_id cannot exceed 128 characters")
	}
	if q.MaxDistanceMiles <= 0 || q.MaxDistanceMiles > 49 {
		return errors.New("max_distance_miles must be between 0 and 49")
	}
	if q.TicketCount < 1 || q.TicketCount > 12 {
		return errors.New("ticket_count must be between 1 and 12")
	}
	if q.Dates.Start == "" || q.Dates.End == "" {
		return errors.New("dates.start and dates.end are required")
	}
	start, err := time.Parse(time.DateOnly, q.Dates.Start)
	if err != nil {
		return errors.New("dates.start must be YYYY-MM-DD")
	}
	end, err := time.Parse(time.DateOnly, q.Dates.End)
	if err != nil {
		return errors.New("dates.end must be YYYY-MM-DD")
	}
	if end.Before(start) {
		return errors.New("dates.end must not be before dates.start")
	}
	if end.Sub(start) > 31*24*time.Hour {
		return errors.New("date range cannot exceed 31 days")
	}
	validProfiles := map[string]bool{"balanced": true, "dead_center": true, "two_thirds_back": true, "aisle": true, "front": true, "back": true, "custom": true}
	if !validProfiles[q.SeatProfile] {
		return fmt.Errorf("unsupported seat_profile %q", q.SeatProfile)
	}
	if q.SeatProfile == "custom" && q.CustomSeatZone == nil {
		return errors.New("custom_seat_zone is required when seat_profile is custom")
	}
	if q.CustomSeatZone != nil {
		zone := q.CustomSeatZone
		if zone.MinimumX < 0 || zone.MaximumX > 1 || zone.MinimumY < 0 || zone.MaximumY > 1 {
			return errors.New("custom_seat_zone coordinates must be between 0 and 1")
		}
		if zone.MinimumX >= zone.MaximumX || zone.MinimumY >= zone.MaximumY {
			return errors.New("custom_seat_zone minimum coordinates must be below maximum coordinates")
		}
	}
	validTimeModes := map[string]bool{"any": true, "inside": true, "outside": true, "before": true, "after": true}
	if !validTimeModes[q.Time.Mode] {
		return fmt.Errorf("unsupported time.mode %q", q.Time.Mode)
	}
	if strings.TrimSpace(q.Time.Timezone) == "" {
		return errors.New("time.timezone is required")
	}
	if _, err := time.LoadLocation(q.Time.Timezone); err != nil {
		return errors.New("time.timezone must be a valid IANA timezone")
	}
	for _, field := range [...]struct{ name, value string }{{"time.start", q.Time.Start}, {"time.end", q.Time.End}} {
		if field.value != "" {
			if _, err := time.Parse("15:04", field.value); err != nil {
				return fmt.Errorf("%s must be HH:MM", field.name)
			}
		}
	}
	if q.Time.Mode != "any" {
		if q.Time.Start == "" && q.Time.Mode != "before" {
			return errors.New("time.start is required for this time mode")
		}
		if q.Time.End == "" && q.Time.Mode != "after" {
			return errors.New("time.end is required for this time mode")
		}
	}
	if q.CandidateLimit < 1 || q.CandidateLimit > 64 {
		return errors.New("candidate_limit must be between 1 and 64")
	}
	if q.MinStartNoticeMinutes < 0 || q.MinStartNoticeMinutes > 1440 {
		return errors.New("min_start_notice_minutes must be between 0 and 1440")
	}
	if q.ExcludeFirstRows < 0 || q.ExcludeFirstRows > 10 {
		return errors.New("exclude_first_rows must be between 0 and 10")
	}
	validConfidence := map[string]bool{"exact_coordinates": true, "rendered_geometry": true, "row_geometry": true, "label_heuristic": true}
	if !validConfidence[q.MinimumGeometryConfidence] {
		return fmt.Errorf("unsupported minimum_geometry_confidence %q", q.MinimumGeometryConfidence)
	}
	return nil
}

func automaticCandidateLimit(DateConstraint) int {
	// Adaptive proof-based stopping normally finishes well before this ceiling.
	// Keeping the full internal allowance available prevents a dense short date
	// range from being less complete than a longer one.
	return 64
}

type Showtime struct {
	ID                string    `json:"id"`
	MovieTitle        string    `json:"movie_title"`
	VenueName         string    `json:"venue_name"`
	VenueTimezone     string    `json:"venue_timezone,omitempty"`
	AuditoriumName    string    `json:"auditorium_name,omitempty"`
	StartsAt          time.Time `json:"starts_at"`
	Format            string    `json:"format"`
	DistanceMiles     float64   `json:"distance_miles"`
	TotalPrice        *float64  `json:"total_price,omitempty"`
	Currency          string    `json:"currency,omitempty"`
	Amenities         []string  `json:"amenities,omitempty"`
	Captions          string    `json:"captions,omitempty"`
	AudioDescription  bool      `json:"audio_description,omitempty"`
	ReservedSeating   bool      `json:"reserved_seating"`
	BookingURL        string    `json:"booking_url"`
	InventoryProvider string    `json:"-"`
	SeatLayoutID      string    `json:"-"`
	ScreenID          string    `json:"-"`
	SiteID            string    `json:"-"`
}

type Seat struct {
	ID     string  `json:"id"`
	Label  string  `json:"label"`
	Row    string  `json:"row"`
	Index  int     `json:"index"`
	X      float64 `json:"x"`
	Y      float64 `json:"y"`
	Type   string  `json:"type"`
	Status string  `json:"status"`
}

type Inventory struct {
	ShowtimeID  string        `json:"showtime_id"`
	Seats       []Seat        `json:"seats"`
	Confidence  string        `json:"confidence"`
	ObservedAt  time.Time     `json:"observed_at"`
	FreshFor    time.Duration `json:"-"`
	TicketPrice *float64      `json:"-"`
	TicketFee   *float64      `json:"-"`
	Currency    string        `json:"-"`
}
type PriceEstimate struct {
	TicketCount    int      `json:"ticket_count"`
	Currency       *string  `json:"currency"`
	TicketPrice    *float64 `json:"ticket_price"`
	FeePerTicket   *float64 `json:"fee_per_ticket"`
	EstimatedTotal *float64 `json:"estimated_total"`
	FeesIncluded   bool     `json:"fees_included"`
	IsEstimate     bool     `json:"is_estimate"`
	Qualification  string   `json:"qualification"`
}

type Recommendation struct {
	Rank           int                `json:"rank"`
	Showtime       Showtime           `json:"showtime"`
	Seats          []Seat             `json:"seats"`
	SeatOptions    []Seat             `json:"seat_options,omitempty"`
	Score          float64            `json:"score"`
	Confidence     string             `json:"confidence"`
	Explanation    []string           `json:"explanation"`
	ScoreBreakdown map[string]float64 `json:"score_breakdown"`
	ProfileMatch   string             `json:"profile_match"`
	VerifiedAt     time.Time          `json:"verified_at"`
	Price          PriceEstimate      `json:"price"`
	BookingURL     string             `json:"booking_url"`
	SeatMap        *SeatMap           `json:"seat_map"`
}

type GeometryPoint struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
}

type SeatMap struct {
	Seats           []Seat        `json:"seats"`
	Target          GeometryPoint `json:"target"`
	PreferredDepth  *DepthRange   `json:"preferred_depth,omitempty"`
	PreferredZone   *SeatZone     `json:"preferred_zone_bounds,omitempty"`
	RecommendedZone []string      `json:"recommended_zone,omitempty"`
	Confidence      string        `json:"confidence"`
	ObservedAt      time.Time     `json:"observed_at"`
}

type DepthRange struct {
	Minimum float64 `json:"minimum"`
	Maximum float64 `json:"maximum"`
}

type Coverage struct {
	DatesRequested          int            `json:"dates_requested"`
	DatesWithScreenings     int            `json:"dates_with_screenings"`
	DatesCompared           int            `json:"dates_compared"`
	RangeBestProven         bool           `json:"range_best_proven"`
	ScreeningsDiscovered    int            `json:"screenings_discovered"`
	ScreeningsPruned        int            `json:"screenings_pruned"`
	InventoriesChecked      int            `json:"inventories_checked"`
	InventoriesFresh        int            `json:"inventories_fresh"`
	InventoriesFailed       int            `json:"inventories_failed"`
	ScreeningsUnavailable   int            `json:"screenings_unavailable"`
	ScreeningsPriceRejected int            `json:"screenings_price_rejected"`
	InventoryFailureReasons map[string]int `json:"inventory_failure_reasons"`
	WinnerVerified          bool           `json:"winner_verified"`
	ProvidersDegraded       int            `json:"providers_degraded"`
	DiscoveryMS             int            `json:"discovery_ms"`
	InventoryMS             int            `json:"inventory_ms"`
	VerificationMS          int            `json:"verification_ms"`
	ElapsedMS               int            `json:"elapsed_ms"`
}
type ResolvedLocation struct {
	Label     string  `json:"label"`
	Latitude  float64 `json:"latitude"`
	Longitude float64 `json:"longitude"`
}

type QueryResponse struct {
	QueryID          string            `json:"query_id"`
	Status           string            `json:"status"`
	GeneratedAt      time.Time         `json:"generated_at"`
	ExpiresAt        time.Time         `json:"expires_at"`
	RefreshUntil     time.Time         `json:"refresh_until"`
	ResolvedLocation *ResolvedLocation `json:"resolved_location,omitempty"`
	Coverage         Coverage          `json:"coverage"`
	Winner           *Recommendation   `json:"winner"`
	Alternatives     []Recommendation  `json:"alternatives"`
	Warnings         []string          `json:"warnings,omitempty"`
}

type ShowtimeQueryResponse struct {
	QueryID          string            `json:"query_id"`
	Status           string            `json:"status"`
	GeneratedAt      time.Time         `json:"generated_at"`
	ExpiresAt        time.Time         `json:"expires_at"`
	ResolvedLocation *ResolvedLocation `json:"resolved_location,omitempty"`
	Coverage         Coverage          `json:"coverage"`
	Showtimes        []Showtime        `json:"showtimes"`
	Warnings         []string          `json:"warnings,omitempty"`
}

type ProviderStatus struct {
	Name          string     `json:"name"`
	Kind          string     `json:"kind"`
	Status        string     `json:"status"`
	Configured    bool       `json:"configured"`
	Message       string     `json:"message,omitempty"`
	Coverage      string     `json:"coverage,omitempty"`
	LocationMode  string     `json:"location_mode,omitempty"`
	LastSuccessAt *time.Time `json:"last_success_at,omitempty"`
}

type MovieSuggestion struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	ReleaseDate string `json:"release_date,omitempty"`
	Year        string `json:"year,omitempty"`
}
