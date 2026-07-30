package providers

import (
	"context"
	"errors"

	"centerseat/backend/internal/domain"
)

type UnavailableInventory struct {
	reason string
}

func NewUnavailableInventory(reason string) *UnavailableInventory {
	return &UnavailableInventory{reason: reason}
}

func (u *UnavailableInventory) Name() string { return "seat-inventory-not-connected" }

func (u *UnavailableInventory) Supports(domain.Showtime) bool { return false }

func (u *UnavailableInventory) GetAvailability(context.Context, domain.Showtime, bool) (domain.Inventory, error) {
	return domain.Inventory{}, errors.New("live seat inventory is not connected")
}

func (u *UnavailableInventory) ProviderStatus(kind string) domain.ProviderStatus {
	return domain.ProviderStatus{
		Name: u.Name(), Kind: kind, Status: "disabled", Configured: false,
		Message: u.reason,
	}
}
