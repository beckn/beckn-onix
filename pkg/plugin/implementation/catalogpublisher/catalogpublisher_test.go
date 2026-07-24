package catalogpublisher

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"testing"

	"github.com/beckn-one/beckn-onix/pkg/model"
	"github.com/beckn-one/beckn-onix/pkg/plugin/definition"
)

// fakeKeyManager returns a fixed Ed25519 keyset for one configured keyID;
// it satisfies definition.KeyManager but only Keyset is ever exercised here.
type fakeKeyManager struct {
	keyID  string
	priv   ed25519.PrivateKey
	pub    ed25519.PublicKey
	failed bool
}

func newFakeKeyManager(t *testing.T, keyID string) *fakeKeyManager {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("generating key: %v", err)
	}
	return &fakeKeyManager{keyID: keyID, priv: priv, pub: pub}
}

func (f *fakeKeyManager) GenerateKeyset() (*model.Keyset, error) { return nil, nil }
func (f *fakeKeyManager) InsertKeyset(ctx context.Context, keyID string, keyset *model.Keyset) error {
	return nil
}
func (f *fakeKeyManager) Keyset(ctx context.Context, keyID string) (*model.Keyset, error) {
	if f.failed || keyID != f.keyID {
		return nil, errNotFound
	}
	return &model.Keyset{
		SigningPrivate: base64.StdEncoding.EncodeToString(f.priv.Seed()),
		SigningPublic:  base64.StdEncoding.EncodeToString(f.pub),
	}, nil
}
func (f *fakeKeyManager) LookupNPKeys(ctx context.Context, subscriberID, uniqueKeyID string) (string, string, error) {
	return "", "", nil
}
func (f *fakeKeyManager) DeleteKeyset(ctx context.Context, keyID string) error { return nil }

var errNotFound = &keyNotFoundError{}

type keyNotFoundError struct{}

func (e *keyNotFoundError) Error() string { return "key not found" }

func validCatalogJSON(id string) json.RawMessage {
	return json.RawMessage(`{"id":"` + id + `","descriptor":{"name":"Test Provider"},"provider":{},"resources":[]}`)
}

func TestNew_RequiresKeyManagerAndKeyID(t *testing.T) {
	km := newFakeKeyManager(t, "k1")

	if _, _, err := New(context.Background(), nil, &Config{KeyID: "k1"}); err == nil {
		t.Fatal("expected error for nil KeyManager")
	}
	if _, _, err := New(context.Background(), km, &Config{}); err == nil {
		t.Fatal("expected error for missing keyID")
	}
	if _, _, err := New(context.Background(), km, &Config{KeyID: "k1"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestPublish_SingleCatalog_ProducesSignedManifestAndIndex(t *testing.T) {
	km := newFakeKeyManager(t, "publisher-key-1")
	p, _, err := New(context.Background(), km, &Config{KeyID: "publisher-key-1", Domain: "example.test"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	req := definition.PublishRequest{
		Catalogs: []definition.CatalogSubmission{
			{
				CatalogID:   "CAT-1",
				SchemaTypes: []string{"retail"},
				Visibility:  definition.PublishVisibility{Public: true},
				Catalog:     validCatalogJSON("CAT-1"),
			},
		},
	}

	result, err := p.Publish(context.Background(), req)
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if len(result.Errors) != 0 {
		t.Fatalf("expected no errors, got %+v", result.Errors)
	}
	if len(result.Catalogs) != 1 || !result.Catalogs[0].Changed || result.Catalogs[0].Version != 1 {
		t.Fatalf("unexpected catalog outcomes: %+v", result.Catalogs)
	}
	if len(result.Manifest) == 0 || len(result.Index) == 0 {
		t.Fatal("expected non-empty manifest and index")
	}

	var manifest dediManifest
	if err := json.Unmarshal(result.Manifest, &manifest); err != nil {
		t.Fatalf("parsing manifest: %v", err)
	}
	if len(manifest.Keys) != 1 || manifest.Keys[0].KID != "publisher-key-1" {
		t.Fatalf("unexpected manifest keys: %+v", manifest.Keys)
	}
	if manifest.Proof == nil || manifest.Proof.Jws == "" {
		t.Fatal("expected manifest to carry a proof")
	}
	if len(manifest.Files) != 1 || manifest.Files[0].Registry != catalogIndexRegistry {
		t.Fatalf("unexpected manifest files: %+v", manifest.Files)
	}

	var index dediIndex
	if err := json.Unmarshal(result.Index, &index); err != nil {
		t.Fatalf("parsing index: %v", err)
	}
	if index.Proof == nil || index.Proof.Jws == "" {
		t.Fatal("expected index to carry a proof")
	}
	if len(index.Records) != 1 {
		t.Fatalf("expected 1 record, got %d", len(index.Records))
	}
	rec := index.Records[0].Details
	if rec.CatalogID != "CAT-1" || rec.Status != "ACTIVE" || rec.Visibility != "public" {
		t.Fatalf("unexpected index record: %+v", rec)
	}
	if len(rec.Parts) != 1 || rec.Parts[0].Digest != "sha-256:"+digestOf(validCatalogJSON("CAT-1")) {
		t.Fatalf("unexpected parts: %+v", rec.Parts)
	}
}

func TestPublish_InvalidSubmissionIsNonFatal(t *testing.T) {
	km := newFakeKeyManager(t, "k1")
	p, _, err := New(context.Background(), km, &Config{KeyID: "k1"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	req := definition.PublishRequest{
		Catalogs: []definition.CatalogSubmission{
			{CatalogID: "", Catalog: validCatalogJSON("bad")},                 // missing catalogId
			{CatalogID: "CAT-OK", Catalog: validCatalogJSON("CAT-OK")},        // valid
			{CatalogID: "CAT-BAD-JSON", Catalog: json.RawMessage(`not json`)}, // invalid JSON
		},
	}

	result, err := p.Publish(context.Background(), req)
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if len(result.Errors) != 2 {
		t.Fatalf("expected 2 non-fatal errors, got %d: %+v", len(result.Errors), result.Errors)
	}
	if len(result.Catalogs) != 1 || result.Catalogs[0].CatalogID != "CAT-OK" {
		t.Fatalf("expected only CAT-OK to succeed, got %+v", result.Catalogs)
	}
	// A partial failure must still produce a validly signed manifest/index.
	if len(result.Manifest) == 0 || len(result.Index) == 0 {
		t.Fatal("expected manifest/index to still be produced despite partial failure")
	}
}

func TestPublish_UnknownKeyIDFails(t *testing.T) {
	km := newFakeKeyManager(t, "k1")
	p, _, err := New(context.Background(), km, &Config{KeyID: "wrong-key"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := p.Publish(context.Background(), definition.PublishRequest{}); err == nil {
		t.Fatal("expected error for unknown keyID")
	}
}

func TestEncodeVisibility(t *testing.T) {
	cases := []struct {
		name string
		in   definition.PublishVisibility
		want string
	}{
		{"public flag", definition.PublishVisibility{Public: true}, "public"},
		{"no networks defaults public", definition.PublishVisibility{}, "public"},
		{"scoped networks", definition.PublishVisibility{Networks: []string{"net-a", "net-b"}}, "networks:net-a,net-b"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := encodeVisibility(c.in); got != c.want {
				t.Errorf("encodeVisibility(%+v) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

func TestCatalogPartURL_PlaceholderWhenUnconfigured(t *testing.T) {
	p := &Publisher{config: &Config{}}
	if got := p.catalogPartURL("CAT-1", "baseline.json"); got != "pending-artifact-store://catalog/CAT-1/baseline.json" {
		t.Errorf("unexpected placeholder URL: %q", got)
	}
	p.config.CatalogBaseURL = "https://cdn.example.com/catalogs/"
	if got := p.catalogPartURL("CAT-1", "baseline.json"); got != "https://cdn.example.com/catalogs/CAT-1/baseline.json" {
		t.Errorf("unexpected configured URL: %q", got)
	}
}

func mustCatalogWithItems(id string, items ...string) json.RawMessage {
	resources := "["
	for i, itemID := range items {
		if i > 0 {
			resources += ","
		}
		resources += `{"id":"` + itemID + `","descriptor":{"name":"` + itemID + `"}}`
	}
	resources += "]"
	return json.RawMessage(`{"id":"` + id + `","descriptor":{"name":"Test"},"provider":{},"resources":` + resources + `}`)
}

func TestPublish_Incremental_NoPriorState_IsBaseline(t *testing.T) {
	km := newFakeKeyManager(t, "k1")
	p, _, err := New(context.Background(), km, &Config{KeyID: "k1"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	result, err := p.Publish(context.Background(), definition.PublishRequest{
		Catalogs: []definition.CatalogSubmission{
			{CatalogID: "CAT-1", Catalog: mustCatalogWithItems("CAT-1", "ITEM-1", "ITEM-2")},
		},
	})
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if len(result.Catalogs) != 1 {
		t.Fatalf("expected 1 outcome, got %+v", result.Catalogs)
	}
	got := result.Catalogs[0]
	if got.Mode != "baseline" || got.Version != 1 || !got.Changed {
		t.Errorf("unexpected outcome: %+v", got)
	}
	if string(got.Content) != string(mustCatalogWithItems("CAT-1", "ITEM-1", "ITEM-2")) {
		t.Errorf("expected baseline content to equal submitted catalog, got %s", got.Content)
	}
}

func TestPublish_Incremental_UnchangedProducesNoOp(t *testing.T) {
	km := newFakeKeyManager(t, "k1")
	p, _, err := New(context.Background(), km, &Config{KeyID: "k1"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	catalog := mustCatalogWithItems("CAT-1", "ITEM-1", "ITEM-2")
	prior := definition.PriorCatalogState{
		Version:      1,
		Catalog:      catalog,
		BaselinePart: &definition.PartRef{URL: "file://baseline.json", Digest: "sha-256:abc"},
	}

	result, err := p.Publish(context.Background(), definition.PublishRequest{
		Catalogs:   []definition.CatalogSubmission{{CatalogID: "CAT-1", Catalog: catalog}},
		PriorState: map[string]definition.PriorCatalogState{"CAT-1": prior},
	})
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}
	got := result.Catalogs[0]
	if got.Mode != "unchanged" || got.Changed || got.Version != 1 || got.Content != nil {
		t.Errorf("expected a no-op outcome, got %+v", got)
	}

	var index dediIndex
	if err := json.Unmarshal(result.Index, &index); err != nil {
		t.Fatalf("parsing index: %v", err)
	}
	rec := index.Records[0].Details
	if rec.Version != 1 || len(rec.Changes) != 0 || rec.Baseline.URL != "file://baseline.json" {
		t.Errorf("unexpected index record carried forward: %+v", rec)
	}
}

func TestPublish_Incremental_ProducesChangeFile(t *testing.T) {
	km := newFakeKeyManager(t, "k1")
	p, _, err := New(context.Background(), km, &Config{KeyID: "k1", CatalogBaseURL: "https://cdn.test/catalogs"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	priorCatalog := mustCatalogWithItems("CAT-1", "ITEM-1", "ITEM-2")
	// ITEM-1 updated (different descriptor.name), ITEM-2 removed, ITEM-3 added.
	nextCatalog := json.RawMessage(`{"id":"CAT-1","descriptor":{"name":"Test"},"provider":{},"resources":[` +
		`{"id":"ITEM-1","descriptor":{"name":"ITEM-1-updated"}},` +
		`{"id":"ITEM-3","descriptor":{"name":"ITEM-3"}}]}`)

	prior := definition.PriorCatalogState{
		Version:      1,
		Catalog:      priorCatalog,
		BaselinePart: &definition.PartRef{URL: "https://cdn.test/catalogs/CAT-1/baseline.json", Digest: "sha-256:abc"},
	}

	result, err := p.Publish(context.Background(), definition.PublishRequest{
		Catalogs:   []definition.CatalogSubmission{{CatalogID: "CAT-1", Catalog: nextCatalog}},
		PriorState: map[string]definition.PriorCatalogState{"CAT-1": prior},
	})
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}
	got := result.Catalogs[0]
	if got.Mode != "change" || !got.Changed || got.Version != 2 {
		t.Fatalf("unexpected outcome: %+v", got)
	}

	var change changeFileDoc
	if err := json.Unmarshal(got.Content, &change); err != nil {
		t.Fatalf("parsing change file: %v", err)
	}
	if change.Version != 2 {
		t.Errorf("change.Version = %d, want 2", change.Version)
	}
	if len(change.Added) != 1 || len(change.Updated) != 1 || len(change.Removed) != 1 {
		t.Fatalf("unexpected change contents: %+v", change)
	}
	if change.Removed[0] != "ITEM-2" {
		t.Errorf("Removed = %v, want [ITEM-2]", change.Removed)
	}
	addedID, _ := resourceID(change.Added[0])
	if addedID != "ITEM-3" {
		t.Errorf("Added[0] id = %q, want ITEM-3", addedID)
	}
	updatedID, _ := resourceID(change.Updated[0])
	if updatedID != "ITEM-1" {
		t.Errorf("Updated[0] id = %q, want ITEM-1", updatedID)
	}

	var index dediIndex
	if err := json.Unmarshal(result.Index, &index); err != nil {
		t.Fatalf("parsing index: %v", err)
	}
	rec := index.Records[0].Details
	if rec.Version != 2 {
		t.Errorf("index record version = %d, want 2", rec.Version)
	}
	if rec.Baseline.URL != "https://cdn.test/catalogs/CAT-1/baseline.json" {
		t.Errorf("expected baseline part carried forward unchanged, got %+v", rec.Baseline)
	}
	if len(rec.Changes) != 1 || rec.Changes[0].URL != "https://cdn.test/catalogs/CAT-1/change-2.json" {
		t.Errorf("unexpected change part: %+v", rec.Changes)
	}
	if len(rec.Parts) != 2 {
		t.Errorf("expected 2 parts (baseline + 1 change), got %d: %+v", len(rec.Parts), rec.Parts)
	}
}

func TestDiffCatalogs(t *testing.T) {
	prior := mustCatalogWithItems("CAT-1", "ITEM-1", "ITEM-2")
	next := json.RawMessage(`{"resources":[{"id":"ITEM-1","descriptor":{"name":"ITEM-1"}},{"id":"ITEM-3","descriptor":{"name":"ITEM-3"}}]}`)

	diff, err := diffCatalogs(prior, next)
	if err != nil {
		t.Fatalf("diffCatalogs: %v", err)
	}
	if len(diff.Added) != 1 || len(diff.Updated) != 0 || len(diff.Removed) != 1 {
		t.Fatalf("unexpected diff: %+v", diff)
	}
	if diff.Removed[0] != "ITEM-2" {
		t.Errorf("Removed = %v, want [ITEM-2]", diff.Removed)
	}
}

func TestDiffCatalogs_NoChangesIsEmpty(t *testing.T) {
	catalog := mustCatalogWithItems("CAT-1", "ITEM-1")
	diff, err := diffCatalogs(catalog, catalog)
	if err != nil {
		t.Fatalf("diffCatalogs: %v", err)
	}
	if !diff.isEmpty() {
		t.Errorf("expected empty diff for identical catalogs, got %+v", diff)
	}
}
