package main

import (
	"context"
	"net/http"
	"testing"
	"time"

	"centerseat/backend/internal/domain"
)

func TestHTTPServerAllowsLiveQueriesToFinishBeforeWebDeadline(t *testing.T) {
	server := newHTTPServer(":0", http.NotFoundHandler())
	const webDeadline = 45 * time.Second
	if server.WriteTimeout <= webDeadline {
		t.Fatalf("API write timeout %s must exceed web deadline %s", server.WriteTimeout, webDeadline)
	}
}

type healthCheckedProvider struct {
	name string
}

func (p *healthCheckedProvider) Name() string { return p.name }

func (p *healthCheckedProvider) Check(context.Context) error { return nil }

func (p *healthCheckedProvider) Discover(context.Context, domain.QueryRequest) ([]domain.Showtime, error) {
	return nil, nil
}

func (p *healthCheckedProvider) Supports(domain.Showtime) bool { return true }

func (p *healthCheckedProvider) GetAvailability(context.Context, domain.Showtime, bool) (domain.Inventory, error) {
	return domain.Inventory{}, nil
}

func TestProviderHealthCheckersDeduplicatesCombinedProvider(t *testing.T) {
	provider := &healthCheckedProvider{name: "combined"}
	checkers := providerHealthCheckers(provider, provider)
	if len(checkers) != 1 || checkers[0].name != provider.name {
		t.Fatalf("expected one health checker for the combined provider, got %#v", checkers)
	}
}
