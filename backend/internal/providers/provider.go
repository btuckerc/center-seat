package providers

import (
	"context"

	"centerseat/backend/internal/domain"
)

type Discovery interface {
	Name() string
	Discover(context.Context, domain.QueryRequest) ([]domain.Showtime, error)
}

type Inventory interface {
	Name() string
	Supports(domain.Showtime) bool
	GetAvailability(context.Context, domain.Showtime, bool) (domain.Inventory, error)
}
