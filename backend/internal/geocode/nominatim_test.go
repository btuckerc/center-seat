package geocode

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestNominatimParsesAndCachesResult(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.URL.Query().Get("format") != "jsonv2" || r.URL.Query().Get("limit") != "1" || r.URL.Query().Get("addressdetails") != "1" || r.URL.Query().Get("countrycodes") != "us,ca" {
			t.Errorf("unexpected query: %s", r.URL.RawQuery)
		}
		if r.Header.Get("User-Agent") != "centerseat-test" {
			t.Errorf("unexpected User-Agent: %q", r.Header.Get("User-Agent"))
		}
		_, _ = w.Write([]byte(`[{"lat":"40.6526006","lon":"-73.9497211","display_name":"Brooklyn, Kings County","address":{"postcode":"11201"}}]`))
	}))
	defer server.Close()
	resolver := NewNominatim(NominatimConfig{BaseURL: server.URL, UserAgent: "centerseat-test", Countries: "us,ca"})
	first, err := resolver.Resolve(context.Background(), "Brooklyn, NY")
	if err != nil || first.Label != "Brooklyn, Kings County" || first.Latitude != 40.6526006 || first.Longitude != -73.9497211 || first.PostalCode != "11201" {
		t.Fatalf("unexpected place: %#v, %v", first, err)
	}
	second, err := resolver.Resolve(context.Background(), " brooklyn,   ny ")
	if err != nil || second != first || calls.Load() != 1 {
		t.Fatalf("cache miss for normalized query: %#v, %v; calls=%d", second, err, calls.Load())
	}
}

func TestNominatimNotFoundAndCoalescesConcurrentLookups(t *testing.T) {
	var calls atomic.Int32
	started := make(chan struct{})
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		close(started)
		<-release
		_, _ = w.Write([]byte(`[]`))
	}))
	defer server.Close()
	resolver := NewNominatim(NominatimConfig{BaseURL: server.URL})
	const count = 6
	var wg sync.WaitGroup
	wg.Add(count)
	errs := make(chan error, count)
	for i := 0; i < count; i++ {
		go func() {
			defer wg.Done()
			_, err := resolver.Resolve(context.Background(), "coalesced-not-found-2026")
			errs <- err
		}()
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("upstream lookup did not start")
	}
	close(release)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != ErrNotFound {
			t.Errorf("expected ErrNotFound, got %v", err)
		}
	}
	if calls.Load() != 1 {
		t.Fatalf("expected one coalesced upstream request, got %d", calls.Load())
	}
	if _, err := resolver.Resolve(context.Background(), "coalesced-not-found-2026"); err != ErrNotFound || calls.Load() != 1 {
		t.Fatalf("negative cache failed: %v, calls=%d", err, calls.Load())
	}
}
