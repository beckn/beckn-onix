package catalogpublisher

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/beckn-one/beckn-onix/pkg/plugin/definition"

	"github.com/beckn/catalog-core/pkg/catalog"
	"github.com/beckn/catalog-core/pkg/catalog/store"
)

// fetchLatest re-fetches catalogID's index entry and its "latest" file the
// way a crawler would -- reachability, digest and signature -- and returns
// the entry.
func fetchLatest(t *testing.T, p *Publisher, catalogID string) catalog.CatalogEntry {
	t.Helper()
	idxRes, err := p.fetcher.FetchIndex(context.Background(), p.IndexURL(), catalog.IndexConditions{})
	if err != nil {
		t.Fatalf("FetchIndex: %v", err)
	}
	entry, ok := catalog.FindCatalog(idxRes.Index, catalogID)
	if !ok {
		t.Fatalf("%s is not in the published index", catalogID)
	}
	if entry.Latest == nil {
		t.Fatalf("%s has no latest pointer", catalogID)
	}
	if _, err := p.fetcher.FetchFile(context.Background(), "example.test", catalogID, *entry.Latest); err != nil {
		t.Fatalf("latest file of %s does not verify against its index entry: %v", catalogID, err)
	}
	return entry
}

// TestPublish_UnchangedRepublishKeepsLatestConsistent covers publishing
// content that has not changed, and then a metadata-only change, with
// Gzip and PublishLatest on (the plugin defaults). Neither produces a
// baseline or change file, but both refresh "latest": it must be written
// to the file the index points at, not to the uncompressed file name.
func TestPublish_UnchangedRepublishKeepsLatestConsistent(t *testing.T) {
	for _, gzip := range []bool{true, false} {
		t.Run(fmt.Sprintf("gzip=%v", gzip), func(t *testing.T) {
			km := newFakeKeyManager(t, "k1")
			km.domain = "example.test"
			bs := newFakeBlobStore()
			p := newVerifiablePublisher(t, km, bs, nil, &Config{SubscriberID: "k1", PublishLatest: true, Gzip: gzip})
			const id = "example.test/CAT-1"
			submit := func(networks ...string) definition.PublishResult {
				t.Helper()
				result, err := p.Publish(context.Background(), definition.PublishRequest{
					Catalogs: []definition.CatalogSubmission{{CatalogID: id, Catalog: validCatalogJSON("CAT-1"), NetworkIds: networks}},
				})
				if err != nil || len(result.Errors) != 0 {
					t.Fatalf("Publish: err=%v errors=%+v", err, result.Errors)
				}
				return result
			}

			submit("net-a")
			fetchLatest(t, p, id)

			unchanged := submit("net-a")
			if unchanged.Catalogs[0].Changed {
				t.Fatalf("expected the identical republish to be unchanged, got mode %q", unchanged.Catalogs[0].Mode)
			}
			fetchLatest(t, p, id)

			metadataOnly := submit("net-a", "net-b")
			if metadataOnly.Catalogs[0].Mode != "metadata" {
				t.Fatalf("expected a metadata-only change, got mode %q", metadataOnly.Catalogs[0].Mode)
			}
			fetchLatest(t, p, id)

			// The other "latest" file name must never appear.
			if _, err := bs.Get(context.Background(), store.LatestFilePath(id, !gzip)); err == nil {
				t.Errorf("a stray %s was written", store.LatestFilePath(id, !gzip))
			}
		})
	}
}

// TestPublish_ConcurrentCallsKeepEveryCatalog publishes many different
// catalogs at once. Every call reads, merges and rewrites the one index,
// so without mutual exclusion later writers drop earlier writers' entries.
func TestPublish_ConcurrentCallsKeepEveryCatalog(t *testing.T) {
	km := newFakeKeyManager(t, "k1")
	km.domain = "example.test"
	p := newVerifiablePublisher(t, km, newFakeBlobStore(), nil, &Config{SubscriberID: "k1", PublishLatest: true, Gzip: true})

	const n = 12
	results := make([]definition.PublishResult, n)
	errs := make([]error, n)
	var wg sync.WaitGroup
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			name := fmt.Sprintf("CAT-%d", i)
			results[i], errs[i] = p.Publish(context.Background(), definition.PublishRequest{
				Catalogs: []definition.CatalogSubmission{{CatalogID: "example.test/" + name, Catalog: validCatalogJSON(name)}},
			})
		}()
	}
	wg.Wait()

	for i := range n {
		if errs[i] != nil || len(results[i].Errors) != 0 {
			t.Errorf("publish %d: err=%v errors=%+v", i, errs[i], results[i].Errors)
		}
	}
	idxRes, err := p.fetcher.FetchIndex(context.Background(), p.IndexURL(), catalog.IndexConditions{})
	if err != nil {
		t.Fatalf("FetchIndex: %v", err)
	}
	if got := len(idxRes.Index.Catalogs); got != n {
		t.Fatalf("index has %d of %d catalogs after concurrent publishes", got, n)
	}
	for i := range n {
		fetchLatest(t, p, fmt.Sprintf("example.test/CAT-%d", i))
	}
}

// TestPublish_WaitingCallStopsWhenItsContextEnds: a call queued behind
// another publish gives up when its own context is cancelled, instead of
// holding the caller indefinitely.
func TestPublish_WaitingCallStopsWhenItsContextEnds(t *testing.T) {
	km := newFakeKeyManager(t, "k1")
	km.domain = "example.test"
	p := newVerifiablePublisher(t, km, newFakeBlobStore(), nil, &Config{SubscriberID: "k1"})

	p.publishLock <- struct{}{} // another publish is in progress
	defer func() { <-p.publishLock }()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := p.Publish(ctx, definitionPublishRequestOneCatalog("example.test/CAT-1"))
	if err == nil {
		t.Fatal("expected Publish to return an error when its context ends while waiting")
	}
}
