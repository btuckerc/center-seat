package providers

import (
	"errors"
	"os"
	"strconv"
	"strings"
	"time"
)

func FromEnvironment() (Discovery, Inventory, error) {
	mode := strings.ToLower(strings.TrimSpace(os.Getenv("CENTERSEAT_PROVIDER_MODE")))
	switch mode {
	case "opencinema":
		provider, err := NewOpenCinema(OpenCinemaConfig{
			BaseURL:        os.Getenv("OPEN_CINEMA_API_BASE_URL"),
			APIKey:         os.Getenv("OPEN_CINEMA_API_KEY"),
			RequestTimeout: 8 * time.Second,
		}, nil)
		if err != nil {
			return nil, nil, err
		}
		return provider, NewUnavailableInventory("Open Cinema supplies live showtimes and checkout links, but not per-seat inventory"), nil
	case "atom":
		provider, err := NewAtom(AtomConfig{
			BaseURL:        os.Getenv("ATOM_API_BASE_URL"),
			APIKey:         os.Getenv("ATOM_API_KEY"),
			PartnerID:      os.Getenv("ATOM_PARTNER_ID"),
			RequestTimeout: 8 * time.Second,
		}, nil)
		if err != nil {
			return nil, nil, err
		}
		return provider, provider, nil
	case "fandango-local":
		environment := strings.ToLower(strings.TrimSpace(os.Getenv("CENTERSEAT_ENV")))
		if environment != "local" && environment != "development" {
			return nil, nil, errors.New("fandango-local is restricted to CENTERSEAT_ENV=local or development and cannot run as a hosted production provider")
		}
		provider, err := NewFandangoLocal(FandangoLocalConfig{
			BaseURL:        os.Getenv("FANDANGO_BASE_URL"),
			RequestTimeout: 8 * time.Second,
			MinimumDelay:   time.Duration(environmentInt("FANDANGO_MINIMUM_DELAY_MS", 50)) * time.Millisecond,
			MaxConcurrency: environmentInt("FANDANGO_MAX_CONCURRENCY", 8),
		}, nil)
		if err != nil {
			return nil, nil, err
		}
		return provider, provider, nil
	case "":
		return nil, nil, errors.New("CENTERSEAT_PROVIDER_MODE is required; set it to opencinema, atom, or fandango-local")
	default:
		return nil, nil, errors.New("unsupported CENTERSEAT_PROVIDER_MODE; supported values are opencinema, atom, and fandango-local")
	}
}

func environmentInt(name string, fallback int) int {
	value, err := strconv.Atoi(strings.TrimSpace(os.Getenv(name)))
	if err != nil || value <= 0 {
		return fallback
	}
	return value
}
