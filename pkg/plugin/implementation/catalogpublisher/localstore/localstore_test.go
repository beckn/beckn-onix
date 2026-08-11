package localstore_test

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"os"
	"testing"

	"github.com/beckn-one/beckn-onix/pkg/model"
	"github.com/beckn-one/beckn-onix/pkg/plugin/definition"
	"github.com/beckn-one/beckn-onix/pkg/plugin/implementation/catalogpublisher"
	"github.com/beckn-one/beckn-onix/pkg/plugin/implementation/catalogpublisher/localstore"
)

// jsonEqual compares two JSON documents structurally, ignoring key order
// (map-backed marshaling no longer preserves the original field order).
func jsonEqual(t *testing.T, a, b json.RawMessage) bool {
	t.Helper()
	var av, bv any
	if err := json.Unmarshal(a, &av); err != nil {
		t.Fatalf("parsing a: %v", err)
	}
	if err := json.Unmarshal(b, &bv); err != nil {
		t.Fatalf("parsing b: %v", err)
	}
	am, _ := json.Marshal(av)
	bm, _ := json.Marshal(bv)
	return string(am) == string(bm)
}

// fakeKeyManager returns a fixed Ed25519 keyset for one configured keyID.
type fakeKeyManager struct {
	keyID string
	priv  ed25519.PrivateKey
	pub   ed25519.PublicKey
}

func newFakeKeyManager(t *testing.T) *fakeKeyManager {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("generating key: %v", err)
	}
	return &fakeKeyManager{keyID: "k1", priv: priv, pub: pub}
}

func (f *fakeKeyManager) GenerateKeyset() (*model.Keyset, error) { return nil, nil }
func (f *fakeKeyManager) InsertKeyset(ctx context.Context, keyID string, keyset *model.Keyset) error {
	return nil
}
func (f *fakeKeyManager) Keyset(ctx context.Context, keyID string) (*model.Keyset, error) {
	return &model.Keyset{
		SubscriberID:   "example.test",
		UniqueKeyID:    f.keyID,
		SigningPrivate: base64.StdEncoding.EncodeToString(f.priv.Seed()),
		SigningPublic:  base64.StdEncoding.EncodeToString(f.pub),
	}, nil
}
func (f *fakeKeyManager) LookupNPKeys(ctx context.Context, subscriberID, uniqueKeyID string) (string, string, error) {
	return "", "", nil
}
func (f *fakeKeyManager) DeleteKeyset(ctx context.Context, keyID string) error { return nil }

func newTestPublisher(t *testing.T) *catalogpublisher.Publisher {
	t.Helper()
	p, _, err := catalogpublisher.New(context.Background(), newFakeKeyManager(t), &catalogpublisher.Config{
		SubscriberID: "k1",
	})
	if err != nil {
		t.Fatalf("catalogpublisher.New: %v", err)
	}
	return p
}

func TestWriteThenLoad_RoundTripsBaseline(t *testing.T) {
	root := t.TempDir()
	p := newTestPublisher(t)

	catalog := json.RawMessage(`{"id":"CAT-1","descriptor":{"name":"Test"},"provider":{},"resources":[{"id":"ITEM-1","descriptor":{"name":"one"}}]}`)
	result, err := p.Publish(context.Background(), definition.PublishRequest{
		Catalogs: []definition.CatalogSubmission{{CatalogID: "example.test/CAT-1", Catalog: catalog}},
	})
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if err := localstore.Write(root, result); err != nil {
		t.Fatalf("Write: %v", err)
	}

	state, err := localstore.Load(root, []string{"example.test/CAT-1"})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	prior, ok := state.PriorState["example.test/CAT-1"]
	if !ok {
		t.Fatal("expected prior state for example.test/CAT-1")
	}
	if string(prior.Catalog) != string(catalog) {
		t.Errorf("reconstructed catalog mismatch:\ngot:  %s\nwant: %s", prior.Catalog, catalog)
	}
	if prior.BaselineFile == nil || prior.BaselineFile.Version != 1 {
		t.Errorf("unexpected baseline file ref: %+v", prior.BaselineFile)
	}
	if prior.EntryVersion != 1 {
		t.Errorf("EntryVersion = %d, want 1", prior.EntryVersion)
	}
	if len(state.CarryForward) != 0 {
		t.Errorf("expected no carry-forward entries, got %+v", state.CarryForward)
	}
}

func TestLoad_NoPriorIndex_ReturnsEmptyState(t *testing.T) {
	root := t.TempDir()
	state, err := localstore.Load(root, []string{"example.test/CAT-1"})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(state.PriorState) != 0 || len(state.CarryForward) != 0 {
		t.Errorf("expected empty state, got %+v", state)
	}
}

func TestWriteThenLoad_IncrementalAndCarryForward(t *testing.T) {
	root := t.TempDir()
	p := newTestPublisher(t)

	// Publish two catalogs.
	catA1 := json.RawMessage(`{"id":"CAT-A","descriptor":{"name":"A"},"provider":{},"resources":[{"id":"ITEM-1","descriptor":{"name":"one"}}]}`)
	catB := json.RawMessage(`{"id":"CAT-B","descriptor":{"name":"B"},"provider":{},"resources":[]}`)
	result, err := p.Publish(context.Background(), definition.PublishRequest{
		Catalogs: []definition.CatalogSubmission{
			{CatalogID: "example.test/CAT-A", Catalog: catA1},
			{CatalogID: "example.test/CAT-B", Catalog: catB},
		},
	})
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if err := localstore.Write(root, result); err != nil {
		t.Fatalf("Write: %v", err)
	}

	// Now publish an update to only CAT-A; CAT-B should carry forward.
	state, err := localstore.Load(root, []string{"example.test/CAT-A"})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(state.CarryForward) != 1 {
		t.Fatalf("expected CAT-B carried forward, got %+v", state.CarryForward)
	}

	catA2 := json.RawMessage(`{"id":"CAT-A","descriptor":{"name":"A"},"provider":{},"resources":[{"id":"ITEM-1","descriptor":{"name":"one-updated"}}]}`)
	result2, err := p.Publish(context.Background(), definition.PublishRequest{
		Catalogs:     []definition.CatalogSubmission{{CatalogID: "example.test/CAT-A", Catalog: catA2}},
		PriorState:   state.PriorState,
		CarryForward: state.CarryForward,
	})
	if err != nil {
		t.Fatalf("Publish (v2): %v", err)
	}
	if result2.Catalogs[0].Mode != "change" || result2.Catalogs[0].Version != 2 {
		t.Fatalf("unexpected outcome: %+v", result2.Catalogs[0])
	}
	if err := localstore.Write(root, result2); err != nil {
		t.Fatalf("Write (v2): %v", err)
	}

	// Reload both: CAT-A should reconstruct to v2 content, CAT-B untouched.
	finalState, err := localstore.Load(root, []string{"example.test/CAT-A", "example.test/CAT-B"})
	if err != nil {
		t.Fatalf("Load (final): %v", err)
	}
	if !jsonEqual(t, finalState.PriorState["example.test/CAT-A"].Catalog, catA2) {
		t.Errorf("CAT-A reconstruction mismatch:\ngot:  %s\nwant: %s", finalState.PriorState["example.test/CAT-A"].Catalog, catA2)
	}
	if !jsonEqual(t, finalState.PriorState["example.test/CAT-B"].Catalog, catB) {
		t.Errorf("CAT-B reconstruction mismatch:\ngot:  %s\nwant: %s", finalState.PriorState["example.test/CAT-B"].Catalog, catB)
	}
	if finalState.PriorState["example.test/CAT-A"].EntryVersion != result2.Catalogs[0].EntryVersion {
		t.Errorf("CAT-A reloaded EntryVersion = %d, want %d", finalState.PriorState["example.test/CAT-A"].EntryVersion, result2.Catalogs[0].EntryVersion)
	}
}

// TestLoad_CompactionKeepsSupersededChangesWithinGracePeriod proves the
// NFH-014 CON-TBD-32 minimum: right after a forced re-baseline
// (compaction), the change file(s) that led up to it must still be
// reconstructed as prior state -- Load must not drop them before the new
// baseline's own next_update has passed.
func TestLoad_CompactionKeepsSupersededChangesWithinGracePeriod(t *testing.T) {
	root := t.TempDir()
	p := newTestPublisher(t)

	cat1 := json.RawMessage(`{"id":"CAT-1","descriptor":{"name":"A"},"provider":{},"resources":[{"id":"ITEM-1","descriptor":{"name":"one"}}]}`)
	result1, err := p.Publish(context.Background(), definition.PublishRequest{
		Catalogs: []definition.CatalogSubmission{{CatalogID: "example.test/CAT-1", Catalog: cat1}},
	})
	if err != nil {
		t.Fatalf("Publish (baseline): %v", err)
	}
	if err := localstore.Write(root, result1); err != nil {
		t.Fatalf("Write (baseline): %v", err)
	}

	state1, err := localstore.Load(root, []string{"example.test/CAT-1"})
	if err != nil {
		t.Fatalf("Load (v1): %v", err)
	}
	cat2 := json.RawMessage(`{"id":"CAT-1","descriptor":{"name":"A"},"provider":{},"resources":[{"id":"ITEM-1","descriptor":{"name":"one-updated"}}]}`)
	result2, err := p.Publish(context.Background(), definition.PublishRequest{
		Catalogs:   []definition.CatalogSubmission{{CatalogID: "example.test/CAT-1", Catalog: cat2}},
		PriorState: state1.PriorState,
	})
	if err != nil {
		t.Fatalf("Publish (change): %v", err)
	}
	if err := localstore.Write(root, result2); err != nil {
		t.Fatalf("Write (change): %v", err)
	}

	state2, err := localstore.Load(root, []string{"example.test/CAT-1"})
	if err != nil {
		t.Fatalf("Load (v2): %v", err)
	}
	result3, err := p.Publish(context.Background(), definition.PublishRequest{
		Catalogs:      []definition.CatalogSubmission{{CatalogID: "example.test/CAT-1", Catalog: cat2}},
		PriorState:    state2.PriorState,
		ForceBaseline: true, // compaction
	})
	if err != nil {
		t.Fatalf("Publish (compaction): %v", err)
	}
	if result3.Catalogs[0].Version != 3 {
		t.Fatalf("expected compacted baseline at version 3, got %+v", result3.Catalogs[0])
	}
	if err := localstore.Write(root, result3); err != nil {
		t.Fatalf("Write (compaction): %v", err)
	}

	state3, err := localstore.Load(root, []string{"example.test/CAT-1"})
	if err != nil {
		t.Fatalf("Load (post-compaction): %v", err)
	}
	prior := state3.PriorState["example.test/CAT-1"]
	if prior.BaselineFile == nil || prior.BaselineFile.Version != 3 {
		t.Fatalf("expected baseline at version 3, got %+v", prior.BaselineFile)
	}
	if len(prior.ChangeFiles) != 1 || prior.ChangeFiles[0].Version != 2 {
		t.Errorf("expected the superseded v2 change file still listed within the grace period, got %+v", prior.ChangeFiles)
	}
}

// TestLoad_CompactionDropsSupersededChangesAfterGracePeriod proves the
// other half of CON-TBD-32: once the compacted baseline's own next_update
// has passed, Load stops listing the change files that led into it.
func TestLoad_CompactionDropsSupersededChangesAfterGracePeriod(t *testing.T) {
	root := t.TempDir()
	km := newFakeKeyManager(t)
	p, _, err := catalogpublisher.New(context.Background(), km, &catalogpublisher.Config{
		SubscriberID: "k1",
		NextUpdateIn: 1, // effectively already-elapsed by the time Load runs
	})
	if err != nil {
		t.Fatalf("catalogpublisher.New: %v", err)
	}

	cat1 := json.RawMessage(`{"id":"CAT-1","descriptor":{"name":"A"},"provider":{},"resources":[{"id":"ITEM-1","descriptor":{"name":"one"}}]}`)
	result1, err := p.Publish(context.Background(), definition.PublishRequest{
		Catalogs: []definition.CatalogSubmission{{CatalogID: "example.test/CAT-1", Catalog: cat1}},
	})
	if err != nil {
		t.Fatalf("Publish (baseline): %v", err)
	}
	if err := localstore.Write(root, result1); err != nil {
		t.Fatalf("Write (baseline): %v", err)
	}
	state1, err := localstore.Load(root, []string{"example.test/CAT-1"})
	if err != nil {
		t.Fatalf("Load (v1): %v", err)
	}

	cat2 := json.RawMessage(`{"id":"CAT-1","descriptor":{"name":"A"},"provider":{},"resources":[{"id":"ITEM-1","descriptor":{"name":"one-updated"}}]}`)
	result2, err := p.Publish(context.Background(), definition.PublishRequest{
		Catalogs:   []definition.CatalogSubmission{{CatalogID: "example.test/CAT-1", Catalog: cat2}},
		PriorState: state1.PriorState,
	})
	if err != nil {
		t.Fatalf("Publish (change): %v", err)
	}
	if err := localstore.Write(root, result2); err != nil {
		t.Fatalf("Write (change): %v", err)
	}
	state2, err := localstore.Load(root, []string{"example.test/CAT-1"})
	if err != nil {
		t.Fatalf("Load (v2): %v", err)
	}

	result3, err := p.Publish(context.Background(), definition.PublishRequest{
		Catalogs:      []definition.CatalogSubmission{{CatalogID: "example.test/CAT-1", Catalog: cat2}},
		PriorState:    state2.PriorState,
		ForceBaseline: true,
	})
	if err != nil {
		t.Fatalf("Publish (compaction): %v", err)
	}
	if err := localstore.Write(root, result3); err != nil {
		t.Fatalf("Write (compaction): %v", err)
	}

	state3, err := localstore.Load(root, []string{"example.test/CAT-1"})
	if err != nil {
		t.Fatalf("Load (post-compaction): %v", err)
	}
	prior := state3.PriorState["example.test/CAT-1"]
	if len(prior.ChangeFiles) != 0 {
		t.Errorf("expected the superseded change file dropped once the grace period elapsed, got %+v", prior.ChangeFiles)
	}
}

// TestWriteThenLoad_Gzip_RoundTrips proves NFH-014 §10.1 end to end
// through localstore: a baseline and a subsequent change file, both
// published gzip-compressed, are written under ".gz"-suffixed filenames
// matching their declared URLs, and Load decompresses them correctly when
// reconstructing prior state -- content and change application must be
// unaffected by compression.
func TestWriteThenLoad_Gzip_RoundTrips(t *testing.T) {
	root := t.TempDir()
	km := newFakeKeyManager(t)
	p, _, err := catalogpublisher.New(context.Background(), km, &catalogpublisher.Config{
		SubscriberID: "k1",
		Gzip:         true,
	})
	if err != nil {
		t.Fatalf("catalogpublisher.New: %v", err)
	}

	catA1 := json.RawMessage(`{"id":"CAT-1","descriptor":{"name":"A"},"provider":{},"resources":[{"id":"ITEM-1","descriptor":{"name":"one"}}]}`)
	result1, err := p.Publish(context.Background(), definition.PublishRequest{
		Catalogs: []definition.CatalogSubmission{{CatalogID: "example.test/CAT-1", Catalog: catA1}},
	})
	if err != nil {
		t.Fatalf("Publish (baseline): %v", err)
	}
	if err := localstore.Write(root, result1); err != nil {
		t.Fatalf("Write (baseline): %v", err)
	}
	if _, err := os.Stat(localstore.CatalogFilePath(root, "example.test/CAT-1", 1, "json.gz")); err != nil {
		t.Fatalf("expected baseline written under a .json.gz filename: %v", err)
	}

	state1, err := localstore.Load(root, []string{"example.test/CAT-1"})
	if err != nil {
		t.Fatalf("Load (v1): %v", err)
	}
	if string(state1.PriorState["example.test/CAT-1"].Catalog) != string(catA1) {
		t.Fatalf("reconstructed baseline mismatch:\ngot:  %s\nwant: %s", state1.PriorState["example.test/CAT-1"].Catalog, catA1)
	}

	catA2 := json.RawMessage(`{"id":"CAT-1","descriptor":{"name":"A"},"provider":{},"resources":[{"id":"ITEM-1","descriptor":{"name":"one-updated"}}]}`)
	result2, err := p.Publish(context.Background(), definition.PublishRequest{
		Catalogs:   []definition.CatalogSubmission{{CatalogID: "example.test/CAT-1", Catalog: catA2}},
		PriorState: state1.PriorState,
	})
	if err != nil {
		t.Fatalf("Publish (change): %v", err)
	}
	if err := localstore.Write(root, result2); err != nil {
		t.Fatalf("Write (change): %v", err)
	}
	if _, err := os.Stat(localstore.CatalogFilePath(root, "example.test/CAT-1", 2, "changes.json.gz")); err != nil {
		t.Fatalf("expected change file written under a .changes.json.gz filename: %v", err)
	}

	state2, err := localstore.Load(root, []string{"example.test/CAT-1"})
	if err != nil {
		t.Fatalf("Load (v2): %v", err)
	}
	if !jsonEqual(t, state2.PriorState["example.test/CAT-1"].Catalog, catA2) {
		t.Errorf("reconstructed post-change catalog mismatch:\ngot:  %s\nwant: %s", state2.PriorState["example.test/CAT-1"].Catalog, catA2)
	}
}

// TestWriteThenLoad_LatestPublished_RoundTripsAndRetireWritesTombstone
// covers CON-TBD-38 end to end through localstore: publishing with
// "latest" enabled sets PriorState.LatestPublished on reload, and
// retiring that catalog afterwards writes a final, retiredAt-carrying
// CatalogFile to the same stable "latest" path.
func TestWriteThenLoad_LatestPublished_RoundTripsAndRetireWritesTombstone(t *testing.T) {
	root := t.TempDir()
	km := newFakeKeyManager(t)
	p, _, err := catalogpublisher.New(context.Background(), km, &catalogpublisher.Config{
		SubscriberID:  "k1",
		PublishLatest: true,
	})
	if err != nil {
		t.Fatalf("catalogpublisher.New: %v", err)
	}

	catalog := json.RawMessage(`{"id":"CAT-1","descriptor":{"name":"A"},"provider":{},"resources":[{"id":"ITEM-1","descriptor":{"name":"one"}}]}`)
	result1, err := p.Publish(context.Background(), definition.PublishRequest{
		Catalogs: []definition.CatalogSubmission{{CatalogID: "example.test/CAT-1", Catalog: catalog}},
	})
	if err != nil {
		t.Fatalf("Publish (baseline): %v", err)
	}
	if err := localstore.Write(root, result1); err != nil {
		t.Fatalf("Write (baseline): %v", err)
	}

	state, err := localstore.Load(root, []string{"example.test/CAT-1"})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	prior, ok := state.PriorState["example.test/CAT-1"]
	if !ok || !prior.LatestPublished {
		t.Fatalf("expected LatestPublished = true after a PublishLatest publish, got %+v", prior)
	}

	result2, err := p.Publish(context.Background(), definition.PublishRequest{
		Retire:     []string{"example.test/CAT-1"},
		PriorState: state.PriorState,
	})
	if err != nil {
		t.Fatalf("Publish (retire): %v", err)
	}
	if len(result2.RetiredLatest) != 1 {
		t.Fatalf("expected 1 RetiredLatest entry, got %+v", result2.RetiredLatest)
	}
	if err := localstore.Write(root, result2); err != nil {
		t.Fatalf("Write (retire): %v", err)
	}

	latestPath := localstore.LatestFilePath(root, "example.test/CAT-1", "json")
	written, err := os.ReadFile(latestPath)
	if err != nil {
		t.Fatalf("expected the final tombstone written to %s: %v", latestPath, err)
	}
	var doc struct {
		RetiredAt *string `json:"retiredAt"`
	}
	if err := json.Unmarshal(written, &doc); err != nil {
		t.Fatalf("parsing final tombstone: %v", err)
	}
	if doc.RetiredAt == nil {
		t.Error("expected the final tombstone at the stable latest URL to carry retiredAt")
	}
}

func TestLoad_RetiredCatalogHasNoPriorState(t *testing.T) {
	root := t.TempDir()
	p := newTestPublisher(t)

	result, err := p.Publish(context.Background(), definition.PublishRequest{Retire: []string{"example.test/CAT-GONE"}})
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if err := localstore.Write(root, result); err != nil {
		t.Fatalf("Write: %v", err)
	}

	state, err := localstore.Load(root, []string{"example.test/CAT-GONE"})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if _, ok := state.PriorState["example.test/CAT-GONE"]; ok {
		t.Error("expected no prior state for a retired catalog")
	}
}

