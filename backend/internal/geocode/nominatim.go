package geocode

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

var ErrNotFound = errors.New("location not found")

type Place struct {
	Label      string
	Latitude   float64
	Longitude  float64
	PostalCode string
}

type Resolver interface {
	Resolve(ctx context.Context, text string) (Place, error)
}

type NominatimConfig struct {
	BaseURL    string
	UserAgent  string
	Countries  string
	HTTPClient *http.Client
}

type Nominatim struct {
	baseURL   string
	userAgent string
	countries string
	client    *http.Client

	mu       sync.Mutex
	cache    map[string]cacheEntry
	flights  map[string]*lookup
	lastCall time.Time
}

const (
	defaultBaseURL   = "https://nominatim.openstreetmap.org"
	defaultUserAgent = "centerseat/1.0 (+https://movies.angl.gg)"
	positiveTTL      = 30 * 24 * time.Hour
	negativeTTL      = 10 * time.Minute
	maxCacheEntries  = 1024
	minimumInterval  = time.Second
	lookupTimeout    = 5 * time.Second
)

type cacheEntry struct {
	place   Place
	err     error
	expires time.Time
}
type lookup struct {
	done  chan struct{}
	place Place
	err   error
}

func NewNominatim(config NominatimConfig) *Nominatim {
	baseURL := strings.TrimRight(strings.TrimSpace(config.BaseURL), "/")
	if baseURL == "" {
		baseURL = defaultBaseURL
	}
	userAgent := strings.TrimSpace(config.UserAgent)
	if userAgent == "" {
		userAgent = defaultUserAgent
	}
	countries := strings.TrimSpace(config.Countries)
	if countries == "" {
		countries = "us"
	}
	client := config.HTTPClient
	if client == nil {
		client = &http.Client{}
	}
	return &Nominatim{
		baseURL: baseURL, userAgent: userAgent, countries: countries, client: client,
		cache: make(map[string]cacheEntry), flights: make(map[string]*lookup),
	}
}

func (n *Nominatim) Resolve(ctx context.Context, text string) (Place, error) {
	key := strings.ToLower(strings.Join(strings.Fields(strings.TrimSpace(text)), " "))
	if key == "" {
		return Place{}, ErrNotFound
	}
	now := time.Now()
	n.mu.Lock()
	if entry, ok := n.cache[key]; ok {
		if now.Before(entry.expires) {
			n.mu.Unlock()
			return entry.place, entry.err
		}
		delete(n.cache, key)
	}
	if pending, ok := n.flights[key]; ok {
		n.mu.Unlock()
		select {
		case <-pending.done:
			return pending.place, pending.err
		case <-ctx.Done():
			return Place{}, ctx.Err()
		}
	}
	pending := &lookup{done: make(chan struct{})}
	n.flights[key] = pending
	n.mu.Unlock()

	requestCtx, cancel := context.WithTimeout(ctx, lookupTimeout)
	place, err := n.lookup(requestCtx, text)
	cancel()

	n.mu.Lock()
	pending.place, pending.err = place, err
	delete(n.flights, key)
	if err == nil || errors.Is(err, ErrNotFound) {
		ttl := positiveTTL
		if errors.Is(err, ErrNotFound) {
			ttl = negativeTTL
		}
		n.cache[key] = cacheEntry{place: place, err: err, expires: time.Now().Add(ttl)}
		if len(n.cache) > maxCacheEntries {
			oldestKey := ""
			var oldest time.Time
			for candidate, entry := range n.cache {
				if oldestKey == "" || entry.expires.Before(oldest) {
					oldestKey, oldest = candidate, entry.expires
				}
			}
			delete(n.cache, oldestKey)
		}
	}
	close(pending.done)
	n.mu.Unlock()
	return place, err
}

func (n *Nominatim) lookup(ctx context.Context, text string) (Place, error) {
	for {
		n.mu.Lock()
		wait := time.Until(n.lastCall.Add(minimumInterval))
		if wait <= 0 {
			n.lastCall = time.Now()
			n.mu.Unlock()
			break
		}
		n.mu.Unlock()

		timer := time.NewTimer(wait)
		select {
		case <-timer.C:
		case <-ctx.Done():
			timer.Stop()
			return Place{}, ctx.Err()
		}
	}

	endpoint := n.baseURL + "/search"
	parsed, err := url.Parse(endpoint)
	if err != nil {
		return Place{}, fmt.Errorf("invalid geocoder base URL: %w", err)
	}
	query := parsed.Query()
	query.Set("format", "jsonv2")
	query.Set("limit", "1")
	query.Set("addressdetails", "1")
	query.Set("countrycodes", n.countries)
	query.Set("q", text)
	parsed.RawQuery = query.Encode()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
	if err != nil {
		return Place{}, fmt.Errorf("create geocoder request: %w", err)
	}
	request.Header.Set("User-Agent", n.userAgent)
	response, err := n.client.Do(request)
	if err != nil {
		return Place{}, fmt.Errorf("geocoder request: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return Place{}, fmt.Errorf("geocoder returned HTTP %d", response.StatusCode)
	}
	var results []struct {
		Latitude    string `json:"lat"`
		Longitude   string `json:"lon"`
		DisplayName string `json:"display_name"`
		Address     struct {
			Postcode string `json:"postcode"`
		} `json:"address"`
	}
	if err := json.NewDecoder(response.Body).Decode(&results); err != nil {
		return Place{}, fmt.Errorf("decode geocoder response: %w", err)
	}
	if len(results) == 0 {
		return Place{}, ErrNotFound
	}
	latitude, err := strconv.ParseFloat(results[0].Latitude, 64)
	if err != nil {
		return Place{}, fmt.Errorf("decode geocoder latitude: %w", err)
	}
	longitude, err := strconv.ParseFloat(results[0].Longitude, 64)
	if err != nil {
		return Place{}, fmt.Errorf("decode geocoder longitude: %w", err)
	}
	return Place{Label: results[0].DisplayName, Latitude: latitude, Longitude: longitude, PostalCode: results[0].Address.Postcode}, nil
}
