package catalogpublisher

// verify_superseded_test.go -- post-write verification when several
// adapter replicas share a blob store. The store lets a replica commit
// only on top of the entry it loaded, so between one call's commit and its
// verification another replica can legitimately have published the next
// entry for the same catalog. Verification must not report that call
// REJECTED: what it published was committed and then built on.

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/beckn-one/beckn-onix/pkg/plugin/definition"

	"github.com/beckn/catalog-core/pkg/catalog"
	"github.com/beckn/catalog-core/pkg/catalog/crawler"
	"github.com/beckn/catalog-core/pkg/catalog/store"
)

// afterIndexCommit runs hook once, right after this replica's first
// successful conditional write of the index: after its commit, before its
// verification.
type afterIndexCommit struct {
	definition.ConditionalCatalogBlobStore
	once sync.Once
	hook func()
}

func (a *afterIndexCommit) PutIfVersion(ctx context.Context, path string, content []byte, ifVersion string) (string, error) {
	v, err := a.ConditionalCatalogBlobStore.PutIfVersion(ctx, path, content, ifVersion)
	if err == nil && path == store.IndexPath() {
		a.once.Do(a.hook)
	}
	return v, err
}

// Replica A commits a change. Before A verifies, replica B publishes the
// next change on top of it. A's call is ACCEPTED, says it was superseded,
// and returns well inside the retry budget; the change file A wrote is
// still listed and still verifies.
func TestVerify_SupersededByAnotherReplica_IsAccepted(t *testing.T) {
	const id = "example.test/CAT-1"
	armed := false
	var replicas []*Publisher
	replicas = newReplicaPublishers(t, 2, func(i int, bs definition.CatalogBlobStore) definition.CatalogBlobStore {
		if i != 0 {
			return bs
		}
		return &afterIndexCommit{ConditionalCatalogBlobStore: bs.(definition.ConditionalCatalogBlobStore), hook: func() {
			if !armed {
				return
			}
			res, err := replicas[1].Publish(context.Background(), definition.PublishRequest{
				Catalogs: []definition.CatalogSubmission{{CatalogID: id, Catalog: catalogNamed("CAT-1", "from B")}},
			})
			if err != nil || len(res.Errors) != 0 {
				t.Errorf("replica B: err=%v errors=%+v", err, res.Errors)
			}
		}}
	})
	// The first publish goes through replica B so that A's hook is still unused.
	if res, err := replicas[1].Publish(context.Background(), definition.PublishRequest{
		Catalogs: []definition.CatalogSubmission{{CatalogID: id, Catalog: catalogNamed("CAT-1", "first")}},
	}); err != nil || len(res.Errors) != 0 {
		t.Fatalf("set-up: err=%v errors=%+v", err, res.Errors)
	}

	armed = true
	start := time.Now()
	res, err := replicas[0].Publish(context.Background(), definition.PublishRequest{
		Catalogs: []definition.CatalogSubmission{{CatalogID: id, Catalog: catalogNamed("CAT-1", "from A")}},
	})
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("replica A: %v", err)
	}
	if len(res.Errors) != 0 {
		t.Fatalf("replica A was reported REJECTED although its change was committed and built on: %+v", res.Errors)
	}
	if got := res.Catalogs[0]; got.Mode != "change" || got.Version != 2 || got.EntryVersion != 2 {
		t.Errorf("replica A outcome = mode %s version %d entryVersion %d, want change, 2, 2", got.Mode, got.Version, got.EntryVersion)
	}
	if len(res.Warnings) != 1 || !strings.Contains(res.Warnings[0], id) || !strings.Contains(res.Warnings[0], "entryVersion 3") {
		t.Errorf("warnings = %v, want one saying %s was superseded by entryVersion 3", res.Warnings, id)
	}
	if elapsed > 2*time.Second {
		t.Errorf("replica A took %v; a superseded entry must not be waited on as if the index were stale", elapsed)
	}
	entry := verifyWholeCatalog(t, replicas[1], id)
	if entry.EntryVersion != 3 || len(entry.Changes) != 2 {
		t.Errorf("index entry: entryVersion %d with %d changes, want 3 and 2", entry.EntryVersion, len(entry.Changes))
	}
}

// Two replicas update one catalog five times each, at once. Every one of
// the ten calls is committed (TestPublish_TwoReplicasSameCatalog_NoUpdateLost
// checks the result), so every one must also be reported ACCEPTED.
func TestVerify_TwoReplicasSameCatalog_EveryCallAccepted(t *testing.T) {
	replicas := newReplicaPublishers(t, 2, nil)
	const id, rounds = "example.test/CAT-1", 5
	var mu sync.Mutex
	var rejected []string
	var wg sync.WaitGroup
	for r := range replicas {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for round := range rounds {
				res, err := replicas[r].Publish(context.Background(), definition.PublishRequest{
					Catalogs: []definition.CatalogSubmission{{CatalogID: id, Catalog: catalogNamed("CAT-1", fmt.Sprintf("replica %d round %d", r, round))}},
				})
				mu.Lock()
				if err != nil {
					rejected = append(rejected, fmt.Sprintf("replica %d round %d: %v", r, round, err))
				}
				for _, e := range res.Errors {
					rejected = append(rejected, fmt.Sprintf("replica %d round %d: %s: %s", r, round, e.Stage, e.Reason))
				}
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if len(rejected) != 0 {
		t.Errorf("%d of %d calls were not accepted:\n%s", len(rejected), len(replicas)*rounds, strings.Join(rejected, "\n"))
	}
	if entry := verifyWholeCatalog(t, replicas[0], id); entry.EntryVersion != int64(len(replicas)*rounds) {
		t.Errorf("entryVersion %d after %d publishes", entry.EntryVersion, len(replicas)*rounds)
	}
}

func signedOutcome(t *testing.T, entry catalog.CatalogEntry, mode string, version int) definition.CatalogPublishOutcome {
	t.Helper()
	raw, err := json.Marshal(entry)
	if err != nil {
		t.Fatal(err)
	}
	return definition.CatalogPublishOutcome{CatalogID: entry.CatalogID, SignedEntry: raw, Mode: mode, Version: version, EntryVersion: int(entry.EntryVersion)}
}

// What verification concludes from the entry the index serves, relative to
// the entry this call signed.
func TestVerifyOutcome_ServedEntryRelativeToTheSignedOne(t *testing.T) {
	p := testPublisher(t)
	const id = "example.test/CAT-1"
	change := catalog.FileEntry{FromVersion: 1, ToVersion: 2, URL: "http://127.0.0.1:1/catalogs/changes/CAT-1.v2.changes.json", Digest: "sha-256:aa", Size: 1}
	mine := catalog.CatalogEntry{CatalogID: id, EntryVersion: 2, Baseline: catalog.FileEntry{Version: 1}, Changes: []catalog.FileEntry{change},
		Signature: catalog.EntrySignature{KeyID: "k1", Value: "mine"}}
	o := signedOutcome(t, mine, "change", 2)
	index := func(e catalog.CatalogEntry) catalog.Index { return catalog.Index{Catalogs: []catalog.CatalogEntry{e}} }

	t.Run("an older entry is a stale index: retried", func(t *testing.T) {
		older := catalog.CatalogEntry{CatalogID: id, EntryVersion: 1, Signature: catalog.EntrySignature{Value: "older"}}
		note, err := p.verifyOutcome(context.Background(), "example.test", index(older), o)
		if err == nil || crawler.IsPermanent(err) || note != "" {
			t.Errorf("note=%q err=%v, want a retryable error", note, err)
		}
	})
	t.Run("a different entry at the same entryVersion is not this call's: retried, then rejected", func(t *testing.T) {
		other := mine
		other.Signature.Value = "someone else's"
		note, err := p.verifyOutcome(context.Background(), "example.test", index(other), o)
		if err == nil || crawler.IsPermanent(err) || note != "" {
			t.Errorf("note=%q err=%v, want a retryable error", note, err)
		}
	})
	t.Run("a newer tombstone: superseded, accepted", func(t *testing.T) {
		tomb := catalog.CatalogEntry{CatalogID: id, EntryVersion: 3, RetiredAt: "2026-11-12T10:00:00Z", Signature: catalog.EntrySignature{Value: "tomb"}}
		note, err := p.verifyOutcome(context.Background(), "example.test", index(tomb), o)
		if err != nil || !strings.Contains(note, "retired") {
			t.Errorf("note=%q err=%v, want accepted with a note that it was retired", note, err)
		}
	})
	t.Run("a newer entry that no longer lists this call's file: superseded, accepted", func(t *testing.T) {
		newer := catalog.CatalogEntry{CatalogID: id, EntryVersion: 4, Baseline: catalog.FileEntry{Version: 3}, Signature: catalog.EntrySignature{Value: "newer"}}
		note, err := p.verifyOutcome(context.Background(), "example.test", index(newer), o)
		if err != nil || !strings.Contains(note, "entryVersion 4") {
			t.Errorf("note=%q err=%v, want accepted with a note naming entryVersion 4", note, err)
		}
	})
	t.Run("a newer entry that lists this call's file with another digest: permanent", func(t *testing.T) {
		swapped := change
		swapped.Digest = "sha-256:bb"
		newer := catalog.CatalogEntry{CatalogID: id, EntryVersion: 3, Baseline: catalog.FileEntry{Version: 1}, Changes: []catalog.FileEntry{swapped, {FromVersion: 2, ToVersion: 3}},
			Signature: catalog.EntrySignature{Value: "newer"}}
		note, err := p.verifyOutcome(context.Background(), "example.test", index(newer), o)
		if err == nil || !crawler.IsPermanent(err) || note != "" {
			t.Errorf("note=%q err=%v, want a permanent error: the immutable file this call published is listed with different content", note, err)
		}
	})
}

// A publish REJECTED at verification is already committed. Repeating it
// changes nothing, so the repeat is an "unchanged" outcome that writes no
// file -- and it must still be REJECTED for as long as a consumer cannot
// fetch what the entry lists. An unchanged or metadata-only outcome
// therefore verifies the baseline and every change file after it, not
// only "latest".
func TestVerify_RepeatOfARejectedPublishStaysRejected(t *testing.T) {
	for _, tc := range []struct {
		name  string
		path  string
		setUp func(w *cachedWorld)
		items []string
	}{
		{"baseline served truncated", store.CatalogFilePath(cachedID, 1, "json", false), func(*cachedWorld) {}, []string{"ITEM-1"}},
		{"change file served tampered", store.CatalogFilePath(cachedID, 2, "changes.json", false), func(w *cachedWorld) { w.publish("ITEM-1") }, []string{"ITEM-1", "ITEM-2"}},
	} {
		for _, latest := range []bool{true, false} {
			t.Run(fmt.Sprintf("%s/publishLatest=%v", tc.name, latest), func(t *testing.T) {
				w := newCachedWorld(t, Config{PublishLatest: latest})
				tc.setUp(w)
				// The server answers this URL with other bytes, whatever is stored.
				w.bs.Put(context.Background(), tc.path, []byte(`{"catalogId":"example.test/CAT-1"`))
				w.srv.freeze(t, tc.path, 0)

				if res := w.publish(tc.items...); len(res.Errors) != 1 || res.Errors[0].Stage != "verify" {
					t.Fatalf("first call: errors = %+v, want one verify error", res.Errors)
				}
				for n := 2; n <= 3; n++ {
					res := w.publish(tc.items...)
					if len(res.Catalogs) != 1 || res.Catalogs[0].Changed {
						t.Fatalf("call %d: outcome = %+v, want unchanged", n, res.Catalogs)
					}
					if len(res.Errors) != 1 || res.Errors[0].Stage != "verify" {
						t.Fatalf("call %d: errors = %+v; the file is still not what the entry lists, the repeat must be REJECTED", n, res.Errors)
					}
				}
				w.srv.thaw()
				if res := w.publish(tc.items...); len(res.Errors) != 0 {
					t.Fatalf("after the server recovered: %+v", res.Errors)
				}
			})
		}
	}
}
