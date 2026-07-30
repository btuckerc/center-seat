package providers

import (
	"errors"
	"os"
	"strings"
	"time"
)

func FromEnvironment() (Discovery, Inventory, error) {
	mode := strings.ToLower(strings.TrimSpace(os.Getenv("CENTERSEAT_PROVIDER_MODE")))
	switch mode {
	case "vista":
		provider, err := NewVista(VistaConfig{
			APIBaseURL:         os.Getenv("VISTA_API_BASE_URL"),
			AuthURL:            os.Getenv("VISTA_AUTH_URL"),
			ClientID:           os.Getenv("VISTA_CLIENT_ID"),
			Username:           os.Getenv("VISTA_USERNAME"),
			Password:           os.Getenv("VISTA_PASSWORD"),
			RegionCode:         os.Getenv("VISTA_REGION_CODE"),
			SiteIDs:            splitCSV(os.Getenv("VISTA_SITE_IDS")),
			BookingURLTemplate: os.Getenv("VISTA_BOOKING_URL_TEMPLATE"),
			RequestTimeout:     8 * time.Second,
		}, nil)
		if err != nil {
			return nil, nil, err
		}
		return provider, provider, nil
	case "":
		return nil, nil, errors.New("CENTERSEAT_PROVIDER_MODE is required; set it to vista for production")
	default:
		return nil, nil, errors.New("unsupported CENTERSEAT_PROVIDER_MODE; supported value is vista")
	}
}

func splitCSV(value string) []string {
	parts := strings.Split(value, ",")
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			result = append(result, trimmed)
		}
	}
	return result
}
