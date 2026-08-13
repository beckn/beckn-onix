package fetch

// bppuri.go — resolves a node's own registered network address (Subscriber.URL)
// from the network registry, for stamping bppUri onto a pushed catalog when
// its own file content doesn't supply it (RFC NFH-014 §Schema Changes:
// catalog.bppId/bppUri "are the same transaction-leg identity fields context
// already carries... included here so a DS can populate them correctly").
//
// Deliberately separate from KeySource/keyCache in verify.go, rather than
// folded into it: this resolves a different field (URL, not a signing key)
// for a different, non-security-critical purpose (an RFC-optional field, not
// a fail-closed verification gate), and keeping it apart means this addition
// carries zero risk to the signature-verification path.

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/beckn-one/beckn-onix/pkg/model"
)

// BppURISource resolves a node's own registered network address. Mirrors
// KeySource's (nodeID, keyID) shape -- the DeDi lookup endpoint requires
// both -- but returns the subscriber's URL instead of its signing key.
type BppURISource func(ctx context.Context, nodeID, keyID string) (string, error)

// RegistryBppURI builds a BppURISource backed by the given registry, with its
// own independent TTL cache (successes only, same reasoning as keyCache: a
// registry outage must be retried, not pinned for the whole TTL). A
// resolution failure is the caller's to treat as non-fatal -- bppUri is an
// RFC-optional field, not worth failing or retrying an otherwise-good sync
// over.
//
// ttl <= 0 uses DefaultKeyCacheTTL.
func RegistryBppURI(reg KeyRegistry, ttl time.Duration) BppURISource {
	if ttl <= 0 {
		ttl = DefaultKeyCacheTTL
	}
	c := &bppURICache{reg: reg, ttl: ttl, now: time.Now, entries: map[keyCacheKey]bppURICacheEntry{}}
	return c.get
}

type bppURICacheEntry struct {
	url       string
	expiresAt time.Time
}

type bppURICache struct {
	reg KeyRegistry
	ttl time.Duration
	now func() time.Time

	mu      sync.Mutex
	entries map[keyCacheKey]bppURICacheEntry
}

func (c *bppURICache) get(ctx context.Context, nodeID, keyID string) (string, error) {
	if c.reg == nil {
		return "", fmt.Errorf("crawler: no registry configured, cannot resolve bppUri for node %q", nodeID)
	}
	if nodeID == "" || keyID == "" {
		return "", fmt.Errorf("crawler: cannot resolve bppUri without both a nodeId and a keyId")
	}
	ck := keyCacheKey{nodeID: nodeID, keyID: keyID}
	if url, ok := c.lookupCached(ck); ok {
		return url, nil
	}
	subs, err := c.reg.Lookup(ctx, &model.Subscription{
		Subscriber: model.Subscriber{SubscriberID: nodeID},
		KeyID:      keyID,
	})
	if err != nil {
		return "", fmt.Errorf("crawler: registry lookup for node %q key %q: %w", nodeID, keyID, err)
	}
	if len(subs) == 0 || subs[0].URL == "" {
		return "", fmt.Errorf("crawler: registry has no URL for node %q key %q", nodeID, keyID)
	}
	url := subs[0].URL
	c.mu.Lock()
	c.entries[ck] = bppURICacheEntry{url: url, expiresAt: c.now().Add(c.ttl)}
	c.mu.Unlock()
	return url, nil
}

func (c *bppURICache) lookupCached(ck keyCacheKey) (string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.entries[ck]
	if !ok || !c.now().Before(e.expiresAt) {
		return "", false
	}
	return e.url, true
}
