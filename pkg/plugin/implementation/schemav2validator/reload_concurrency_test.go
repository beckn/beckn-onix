package schemav2validator

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
)

// Validate must stay correct while the TTL reload swaps the action index
// underneath it: a request either sees the old spec or the new one, never a
// half-built index, and the reload must not race with validation.
func TestValidate_ConcurrentWithReload(t *testing.T) {
	// Every reload serves a spec that defines "search" (alternating with a
	// byte-different copy), so a rejected search can only come from the swap.
	var fetches atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		spec := testSpec
		if fetches.Add(1)%2 == 0 {
			spec += "\n# reloaded\n"
		}
		_, _ = w.Write([]byte(spec))
	}))
	defer server.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	v, _, err := New(ctx, &Config{Type: "url", Location: server.URL, CacheTTL: 3600})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	search := []byte(`{"context":{"action":"search","domain":"retail"},"message":{}}`)
	stop := make(chan struct{})
	var wg sync.WaitGroup
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				if err := v.Validate(context.Background(), nil, search); err != nil {
					t.Errorf("search rejected during reload: %v", err)
					return
				}
			}
		}()
	}
	for i := 0; i < 40; i++ {
		v.reloadAllSpecs(context.Background())
	}
	if fetches.Load() < 41 {
		t.Errorf("spec fetched %d times, want one per load", fetches.Load())
	}
	close(stop)
	wg.Wait()
}
