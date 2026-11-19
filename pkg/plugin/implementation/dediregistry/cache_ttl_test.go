package dediregistry

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/beckn-one/beckn-onix/pkg/model"
)

// ttlRecordingCache remembers the TTL of every Set.
type ttlRecordingCache struct {
	mu   sync.Mutex
	ttls []time.Duration
}

func (c *ttlRecordingCache) Get(context.Context, string) (string, error) { return "", nil }
func (c *ttlRecordingCache) Set(_ context.Context, _, _ string, ttl time.Duration) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.ttls = append(c.ttls, ttl)
	return nil
}
func (c *ttlRecordingCache) Delete(context.Context, string) error { return nil }
func (c *ttlRecordingCache) Clear(context.Context) error          { return nil }

// A record's "ttl" is a number of seconds taken from the registry response.
// One too large for a time.Duration must not wrap around: a negative TTL
// reaches Redis as "no expiry", so the signing key would be cached forever
// and a later revocation never seen.
func TestLookup_RecordTTLThatOverflowsFallsBackToConfiguredTTL(t *testing.T) {
	for _, tc := range []struct {
		ttlJSON string
		want    time.Duration
	}{
		{"300", 300 * time.Second}, // honoured as before
		{"1e10", 7 * time.Minute},  // wraps negative as a Duration
		{"1e12", 7 * time.Minute},  // wraps to an arbitrary positive value
		{"9.3e9", 7 * time.Minute}, // just past the largest representable Duration
		{"-5", 7 * time.Minute},    // not positive: configured TTL, as before
	} {
		t.Run(tc.ttlJSON, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				fmt.Fprintf(w, `{"data":{"ttl":%s,"details":{"subscriber_id":"bpp.example","signing_public_key":"pk"}}}`, tc.ttlJSON)
			}))
			defer srv.Close()
			cache := &ttlRecordingCache{}
			c, _, err := New(context.Background(), cache, &Config{URL: srv.URL, CacheTTL: 7 * time.Minute})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := c.Lookup(context.Background(), &model.Subscription{Subscriber: model.Subscriber{SubscriberID: "bpp.example"}, KeyID: "k1"}); err != nil {
				t.Fatalf("Lookup: %v", err)
			}
			if len(cache.ttls) != 1 {
				t.Fatalf("cache.Set called %d times", len(cache.ttls))
			}
			if got := cache.ttls[0]; got != tc.want {
				t.Errorf("record ttl %s cached for %v, want %v", tc.ttlJSON, got, tc.want)
			}
		})
	}
}
