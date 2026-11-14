package schemaversionmediator

import (
	"fmt"
	"sync"
	"testing"
	"time"
)

// An entry that expires and is fetched again must occupy one slot, not one
// slot per refresh. Otherwise the insertion-order list fills with copies of
// the hot key and every other insert evicts a live entry while the cache is
// far below its capacity.
func TestArtifactCache_RefreshedKeyDoesNotEvictLiveEntries(t *testing.T) {
	c := newArtifactCache(20*time.Millisecond, 20*time.Millisecond, 3)
	hot := &TranslationArtifact{Content: []byte("hot")}

	// The hot key expires and is refreshed more times than the cache has slots.
	for i := 0; i < 5; i++ {
		c.set("hot", hot)
		time.Sleep(30 * time.Millisecond)
		if _, found := c.get("hot"); found {
			t.Fatalf("round %d: entry should have expired", i)
		}
	}

	// Long TTL from here on, so nothing below expires.
	c.positiveTTL = time.Hour
	c.set("hot", hot)
	c.set("b", &TranslationArtifact{Content: []byte("b")})
	c.set("c", &TranslationArtifact{Content: []byte("c")})

	// Three distinct keys in a cache of three: all must be present.
	for _, k := range []string{"hot", "b", "c"} {
		if _, found := c.get(k); !found {
			t.Errorf("live entry %q was evicted although the cache holds only 3 distinct keys (capacity 3)", k)
		}
	}
	if len(c.keys) != len(c.entries) {
		t.Errorf("insertion-order list has %d keys for %d entries", len(c.keys), len(c.entries))
	}
}

// get and set are called from every request; hammer them together.
func TestArtifactCache_ConcurrentGetSet(t *testing.T) {
	c := newArtifactCache(time.Millisecond, time.Millisecond, 8)
	var wg sync.WaitGroup
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < 500; i++ {
				k := fmt.Sprintf("k%d", (w+i)%12)
				if _, found := c.get(k); !found {
					c.set(k, &TranslationArtifact{Content: []byte(k)})
				}
			}
		}(w)
	}
	wg.Wait()
	c.mu.RLock()
	defer c.mu.RUnlock()
	if len(c.entries) > c.maxEntries {
		t.Errorf("cache holds %d entries, capacity %d", len(c.entries), c.maxEntries)
	}
	if len(c.keys) > c.maxEntries {
		t.Errorf("insertion-order list holds %d keys, capacity %d", len(c.keys), c.maxEntries)
	}
}
