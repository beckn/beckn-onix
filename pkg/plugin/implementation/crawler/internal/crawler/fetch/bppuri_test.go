package fetch

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/beckn-one/beckn-onix/pkg/model"
)

func TestRegistryBppURI(t *testing.T) {
	t.Run("resolves the subscriber's URL", func(t *testing.T) {
		reg := &fakeRegistry{subs: []model.Subscription{{
			Subscriber: model.Subscriber{SubscriberID: "publisher.example.com", URL: "https://publisher.example.com/bpp"},
			KeyID:      "pub-key-1",
		}}}
		resolve := RegistryBppURI(reg, time.Minute)
		got, err := resolve(context.Background(), "publisher.example.com", "pub-key-1")
		if err != nil {
			t.Fatal(err)
		}
		if got != "https://publisher.example.com/bpp" {
			t.Fatalf("got %q", got)
		}
		if reg.gotSubscriberID != "publisher.example.com" || reg.gotKeyID != "pub-key-1" {
			t.Fatalf("lookup got subscriberID=%q keyID=%q", reg.gotSubscriberID, reg.gotKeyID)
		}
	})

	t.Run("missing nodeID or keyID errors without calling the registry", func(t *testing.T) {
		reg := &fakeRegistry{subs: []model.Subscription{{Subscriber: model.Subscriber{URL: "https://x"}}}}
		resolve := RegistryBppURI(reg, time.Minute)
		if _, err := resolve(context.Background(), "", "key"); err == nil {
			t.Fatal("expected an error for missing nodeID")
		}
		if _, err := resolve(context.Background(), "node", ""); err == nil {
			t.Fatal("expected an error for missing keyID")
		}
		if reg.calls != 0 {
			t.Fatalf("expected no registry calls, got %d", reg.calls)
		}
	})

	t.Run("nil registry errors", func(t *testing.T) {
		resolve := RegistryBppURI(nil, time.Minute)
		if _, err := resolve(context.Background(), "node", "key"); err == nil {
			t.Fatal("expected an error for nil registry")
		}
	})

	t.Run("registry error is surfaced, not cached", func(t *testing.T) {
		reg := &fakeRegistry{err: errors.New("connection refused")}
		resolve := RegistryBppURI(reg, time.Minute)
		if _, err := resolve(context.Background(), "node", "key"); err == nil {
			t.Fatal("expected an error")
		}
		reg.err = nil
		reg.subs = []model.Subscription{{Subscriber: model.Subscriber{URL: "https://x"}}}
		got, err := resolve(context.Background(), "node", "key")
		if err != nil {
			t.Fatal(err)
		}
		if got != "https://x" {
			t.Fatalf("got %q", got)
		}
		if reg.calls != 2 {
			t.Fatalf("expected the failed lookup to retry rather than be cached, got %d calls", reg.calls)
		}
	})

	t.Run("no URL in the registry answer errors", func(t *testing.T) {
		reg := &fakeRegistry{subs: []model.Subscription{{Subscriber: model.Subscriber{SubscriberID: "node"}}}}
		resolve := RegistryBppURI(reg, time.Minute)
		if _, err := resolve(context.Background(), "node", "key"); err == nil {
			t.Fatal("expected an error for empty URL")
		}
	})

	t.Run("a resolved URL is cached until the TTL expires", func(t *testing.T) {
		reg := &fakeRegistry{subs: []model.Subscription{{Subscriber: model.Subscriber{URL: "https://x"}}}}
		now := time.Now()
		c := &bppURICache{reg: reg, ttl: time.Minute, now: func() time.Time { return now }, entries: map[keyCacheKey]bppURICacheEntry{}}

		if _, err := c.get(context.Background(), "node", "key"); err != nil {
			t.Fatal(err)
		}
		if _, err := c.get(context.Background(), "node", "key"); err != nil {
			t.Fatal(err)
		}
		if reg.calls != 1 {
			t.Fatalf("expected the cache hit to skip the registry, got %d calls", reg.calls)
		}

		now = now.Add(2 * time.Minute)
		if _, err := c.get(context.Background(), "node", "key"); err != nil {
			t.Fatal(err)
		}
		if reg.calls != 2 {
			t.Fatalf("expected expiry to trigger a fresh lookup, got %d calls", reg.calls)
		}
	})
}
