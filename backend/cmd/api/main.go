package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"centerseat/backend/internal/httpapi"
	"centerseat/backend/internal/providers"
	"centerseat/backend/internal/service"
)

const liveQueryWriteTimeout = 60 * time.Second

func main() {
	if len(os.Args) > 1 && os.Args[1] == "healthcheck" {
		client := &http.Client{Timeout: 2 * time.Second}
		response, err := client.Get("http://127.0.0.1:8080/healthz")
		if err != nil || response.StatusCode != http.StatusOK {
			os.Exit(1)
		}
		_ = response.Body.Close()
		return
	}
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	discovery, inventory, err := providers.FromEnvironment()
	if err != nil {
		logger.Error("provider configuration rejected", "error", err)
		os.Exit(78)
	}
	probeContext, stopProbes := context.WithCancel(context.Background())
	defer stopProbes()
	go monitorProviders(probeContext, logger, discovery, inventory, 30*time.Second)
	svc := service.New(discovery, inventory, 8)
	api := httpapi.New(svc, logger)
	address := os.Getenv("CENTERSEAT_HTTP_ADDR")
	if address == "" {
		address = ":8080"
	}
	server := newHTTPServer(address, api.Handler())
	go func() {
		logger.Info("centerseat api listening", "address", address)
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("server stopped", "error", err)
			os.Exit(1)
		}
	}()
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	<-stop
	stopProbes()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = server.Shutdown(ctx)
}

func newHTTPServer(address string, handler http.Handler) *http.Server {
	return &http.Server{
		Addr:              address,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      liveQueryWriteTimeout,
		IdleTimeout:       60 * time.Second,
	}
}

type namedHealthChecker struct {
	name    string
	checker providers.HealthChecker
}

func providerHealthCheckers(discovery providers.Discovery, inventory providers.Inventory) []namedHealthChecker {
	result := make([]namedHealthChecker, 0, 2)
	seen := map[string]bool{}
	add := func(name string, candidate any) {
		checker, ok := candidate.(providers.HealthChecker)
		if !ok || seen[name] {
			return
		}
		seen[name] = true
		result = append(result, namedHealthChecker{name: name, checker: checker})
	}
	add(discovery.Name(), discovery)
	add(inventory.Name(), inventory)
	return result
}

func monitorProviders(ctx context.Context, logger *slog.Logger, discovery providers.Discovery, inventory providers.Inventory, interval time.Duration) {
	if interval <= 0 {
		interval = 30 * time.Second
	}
	checkers := providerHealthCheckers(discovery, inventory)
	for {
		for _, provider := range checkers {
			probe, cancel := context.WithTimeout(ctx, 5*time.Second)
			err := provider.checker.Check(probe)
			cancel()
			if err != nil {
				logger.Warn("provider probe failed; API remains available", "provider", provider.name, "error", err)
			} else {
				logger.Info("provider probe succeeded", "provider", provider.name)
			}
		}
		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}
