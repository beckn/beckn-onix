package catalogpublisher

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/beckn-one/beckn-onix/pkg/plugin/definition"
	"github.com/beckn-one/beckn-onix/pkg/plugin/implementation/localcatalogblobstore"

	"github.com/beckn/catalog-core/pkg/catalog"
	"github.com/beckn/catalog-core/pkg/catalog/store"
)

// newReplicaPublishers builds n Publishers the way n adapter replicas
// sharing one volume are: each has its own blob store value, its own
// catalog store and its own publishLock. All they share is the
// directory (and the signing key and public URL, as replicas of one
// subscriber do). wrap, if set, wraps replica i's blob store.
func newReplicaPublishers(t *testing.T, n int, wrap func(i int, bs definition.CatalogBlobStore) definition.CatalogBlobStore) []*Publisher {
	t.Helper()
	return newReplicaPublishersAt(t, t.TempDir(), n, wrap)
}

// newReplicaPublishersAt is newReplicaPublishers over a blob root the
// caller chose, so it can look at the directory afterwards.
func newReplicaPublishersAt(t *testing.T, root string, n int, wrap func(i int, bs definition.CatalogBlobStore) definition.CatalogBlobStore) []*Publisher {
	t.Helper()
	srv := httptest.NewServer(http.FileServer(http.Dir(root)))
	t.Cleanup(srv.Close)
	km := newFakeKeyManager(t, "k1")
	km.domain = "example.test"
	out := make([]*Publisher, n)
	for i := range n {
		var bs definition.CatalogBlobStore = localcatalogblobstore.New(root)
		if wrap != nil {
			bs = wrap(i, bs)
		}
		cfg := &Config{SubscriberID: "k1", PublishLatest: true, Gzip: true, PublicBaseURL: srv.URL, AllowPrivateVerifyHosts: true}
		p, _, err := New(context.Background(), km, bs, fakeVerifyRegistry{PubB64: base64.StdEncoding.EncodeToString(km.pub)}, nil, cfg)
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		out[i] = p
	}
	return out
}

// verifyWholeCatalog re-fetches catalogID's entry and every file it
// lists -- baseline, each change file and latest -- checking each
// against the digest in the entry, as a crawler would.
func verifyWholeCatalog(t *testing.T, p *Publisher, catalogID string) catalog.CatalogEntry {
	t.Helper()
	entry := fetchLatest(t, p, catalogID)
	files := append([]catalog.FileEntry{entry.Baseline}, entry.Changes...)
	for _, fe := range files {
		if _, err := p.fetcher.FetchFile(context.Background(), "example.test", catalogID, fe); err != nil {
			t.Errorf("%s: %s does not verify against its index entry: %v", catalogID, fe.URL, err)
		}
	}
	return entry
}

func catalogNamed(id, name string) []byte {
	return []byte(`{"id":"` + id + `","descriptor":{"name":"` + name + `"},"provider":{},"resources":[]}`)
}

// Sixteen publishes of sixteen different catalogs arrive at once, split
// across two replicas that share a blob root but no lock. Every one
// must end up in the index and verify. beckn-onix#952.
func TestPublish_TwoReplicasOneBlobRoot_KeepEveryCatalog(t *testing.T) {
	replicas := newReplicaPublishers(t, 2, nil)
	const n = 16
	results := make([]definition.PublishResult, n)
	errs := make([]error, n)
	var wg sync.WaitGroup
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			name := fmt.Sprintf("CAT-%d", i)
			results[i], errs[i] = replicas[i%2].Publish(context.Background(), definition.PublishRequest{
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
	idxRes, err := replicas[0].fetcher.FetchIndex(context.Background(), replicas[0].IndexURL(), catalog.IndexConditions{})
	if err != nil {
		t.Fatalf("FetchIndex: %v", err)
	}
	if got := len(idxRes.Index.Catalogs); got != n {
		t.Fatalf("index has %d of %d catalogs after publishes through two replicas", got, n)
	}
	for i := range n {
		verifyWholeCatalog(t, replicas[1], fmt.Sprintf("example.test/CAT-%d", i))
	}
}

// Two replicas update the same catalog at the same time, five times
// each, every time with new content. Each call's change must be built
// on the other's, not replace it: ten content versions, ten entry
// versions, and a baseline and change chain that verifies end to end.
func TestPublish_TwoReplicasSameCatalog_NoUpdateLost(t *testing.T) {
	replicas := newReplicaPublishers(t, 2, nil)
	const id, rounds = "example.test/CAT-1", 5
	var wg sync.WaitGroup
	for r := range replicas {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for round := range rounds {
				_, err := replicas[r].Publish(context.Background(), definition.PublishRequest{
					Catalogs: []definition.CatalogSubmission{{CatalogID: id, Catalog: catalogNamed("CAT-1", fmt.Sprintf("replica %d round %d", r, round))}},
				})
				if err != nil {
					t.Errorf("replica %d round %d: %v", r, round, err)
				}
			}
		}()
	}
	wg.Wait()
	entry := verifyWholeCatalog(t, replicas[0], id)
	total := int64(len(replicas) * rounds)
	version := entry.Baseline.Version
	if n := len(entry.Changes); n > 0 {
		version = entry.Changes[n-1].EffectiveVersion()
	}
	if entry.EntryVersion != total || version != total {
		t.Fatalf("entryVersion %d, content version %d after %d publishes of new content: an update was lost", entry.EntryVersion, version, total)
	}
}

// hookedBlobStore runs beforeIndexWrite once, just before its first
// conditional write of the index.
type hookedBlobStore struct {
	definition.ConditionalCatalogBlobStore
	once             sync.Once
	beforeIndexWrite func()
}

func (h *hookedBlobStore) PutIfVersion(ctx context.Context, path string, content []byte, ifVersion string) (string, error) {
	if path == store.IndexPath() {
		h.once.Do(h.beforeIndexWrite)
	}
	return h.ConditionalCatalogBlobStore.PutIfVersion(ctx, path, content, ifVersion)
}

// Replica B publishes the catalog after replica A has loaded its prior
// state and signed against it, but before A writes the index. A must
// notice, start again from B's state, and publish its content as the
// next version -- not replace B's entry with one computed from nothing.
func TestPublish_OtherReplicaCommitsFirst_Recomputes(t *testing.T) {
	const id = "example.test/CAT-1"
	var replicas []*Publisher
	replicas = newReplicaPublishers(t, 2, func(i int, bs definition.CatalogBlobStore) definition.CatalogBlobStore {
		if i != 0 {
			return bs
		}
		return &hookedBlobStore{ConditionalCatalogBlobStore: bs.(definition.ConditionalCatalogBlobStore), beforeIndexWrite: func() {
			res, err := replicas[1].Publish(context.Background(), definition.PublishRequest{
				Catalogs: []definition.CatalogSubmission{{CatalogID: id, Catalog: catalogNamed("CAT-1", "from B")}},
			})
			if err != nil || len(res.Errors) != 0 {
				t.Errorf("replica B: err=%v errors=%+v", err, res.Errors)
			}
		}}
	})
	res, err := replicas[0].Publish(context.Background(), definition.PublishRequest{
		Catalogs: []definition.CatalogSubmission{{CatalogID: id, Catalog: catalogNamed("CAT-1", "from A")}},
	})
	if err != nil || len(res.Errors) != 0 {
		t.Fatalf("replica A: err=%v errors=%+v", err, res.Errors)
	}
	if got := res.Catalogs[0]; got.Mode != "change" || got.Version != 2 || got.EntryVersion != 2 {
		t.Errorf("replica A reported mode=%s version=%d entryVersion=%d, want a change to version 2 on top of B's baseline", got.Mode, got.Version, got.EntryVersion)
	}
	entry := verifyWholeCatalog(t, replicas[1], id)
	if entry.EntryVersion != 2 || entry.Baseline.Version != 1 || len(entry.Changes) != 1 {
		t.Errorf("index entry: entryVersion=%d baseline.version=%d changes=%d", entry.EntryVersion, entry.Baseline.Version, len(entry.Changes))
	}
}
