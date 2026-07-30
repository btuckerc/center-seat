package providers

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"
)

func FromEnvironment() (Discovery, Inventory, error) {
	mode := strings.ToLower(strings.TrimSpace(os.Getenv("CENTERSEAT_PROVIDER_MODE")))
	switch mode {
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
		probeContext, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := provider.Check(probeContext); err != nil {
			return nil, nil, fmt.Errorf("Atom startup probe failed: %w", err)
		}
		return provider, provider, nil
	case "":
		return nil, nil, errors.New("CENTERSEAT_PROVIDER_MODE is required; set it to atom for production")
	default:
		return nil, nil, errors.New("unsupported CENTERSEAT_PROVIDER_MODE; supported value is atom")
	}
}
