package localstore

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"testing"
	"time"

	"github.com/beckn-one/beckn-onix/pkg/model"
	"github.com/beckn-one/beckn-onix/pkg/plugin/definition"
	"github.com/beckn-one/beckn-onix/pkg/plugin/implementation/catalogpublisher"
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
		SubscriberID:   "k1",
		FileValidityIn: 24 * time.Hour,
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
	if err := Write(root, result); err != nil {
		t.Fatalf("Write: %v", err)
	}

	state, err := Load(root, []string{"example.test/CAT-1"})
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
	if state.PriorIndexVersion != 1 {
		t.Errorf("PriorIndexVersion = %d, want 1", state.PriorIndexVersion)
	}
	if len(state.CarryForward) != 0 {
		t.Errorf("expected no carry-forward entries, got %+v", state.CarryForward)
	}
}

func TestLoad_NoPriorIndex_ReturnsEmptyState(t *testing.T) {
	root := t.TempDir()
	state, err := Load(root, []string{"example.test/CAT-1"})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(state.PriorState) != 0 || len(state.CarryForward) != 0 || state.PriorIndexVersion != 0 {
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
	if err := Write(root, result); err != nil {
		t.Fatalf("Write: %v", err)
	}

	// Now publish an update to only CAT-A; CAT-B should carry forward.
	state, err := Load(root, []string{"example.test/CAT-A"})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(state.CarryForward) != 1 {
		t.Fatalf("expected CAT-B carried forward, got %+v", state.CarryForward)
	}

	catA2 := json.RawMessage(`{"id":"CAT-A","descriptor":{"name":"A"},"provider":{},"resources":[{"id":"ITEM-1","descriptor":{"name":"one-updated"}}]}`)
	result2, err := p.Publish(context.Background(), definition.PublishRequest{
		Catalogs:          []definition.CatalogSubmission{{CatalogID: "example.test/CAT-A", Catalog: catA2}},
		PriorState:        state.PriorState,
		CarryForward:      state.CarryForward,
		PriorIndexVersion: state.PriorIndexVersion,
	})
	if err != nil {
		t.Fatalf("Publish (v2): %v", err)
	}
	if result2.Catalogs[0].Mode != "change" || result2.Catalogs[0].Version != 2 {
		t.Fatalf("unexpected outcome: %+v", result2.Catalogs[0])
	}
	if err := Write(root, result2); err != nil {
		t.Fatalf("Write (v2): %v", err)
	}

	// Reload both: CAT-A should reconstruct to v2 content, CAT-B untouched.
	finalState, err := Load(root, []string{"example.test/CAT-A", "example.test/CAT-B"})
	if err != nil {
		t.Fatalf("Load (final): %v", err)
	}
	if !jsonEqual(t, finalState.PriorState["example.test/CAT-A"].Catalog, catA2) {
		t.Errorf("CAT-A reconstruction mismatch:\ngot:  %s\nwant: %s", finalState.PriorState["example.test/CAT-A"].Catalog, catA2)
	}
	if !jsonEqual(t, finalState.PriorState["example.test/CAT-B"].Catalog, catB) {
		t.Errorf("CAT-B reconstruction mismatch:\ngot:  %s\nwant: %s", finalState.PriorState["example.test/CAT-B"].Catalog, catB)
	}
	if finalState.PriorIndexVersion != result2.IndexVersion {
		t.Errorf("PriorIndexVersion = %d, want %d", finalState.PriorIndexVersion, result2.IndexVersion)
	}
}

func TestLoad_RetiredCatalogHasNoPriorState(t *testing.T) {
	root := t.TempDir()
	p := newTestPublisher(t)

	result, err := p.Publish(context.Background(), definition.PublishRequest{Retire: []string{"example.test/CAT-GONE"}})
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if err := Write(root, result); err != nil {
		t.Fatalf("Write: %v", err)
	}

	state, err := Load(root, []string{"example.test/CAT-GONE"})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if _, ok := state.PriorState["example.test/CAT-GONE"]; ok {
		t.Error("expected no prior state for a retired catalog")
	}
}
