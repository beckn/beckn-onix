package catalogpublisher

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"testing"
	"time"

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

func TestPublish_SingleCatalog_ProducesManifestAndIndex(t *testing.T) {
	km := newFakeKeyManager(t, "publisher-key-1")
	p, _, err := New(context.Background(), km, &Config{
		KeyID:          "publisher-key-1",
		Domain:         "example.test",
		IndexSchemaURL: "https://example.test/schemas/catalog-index.json",
		NextUpdateIn:   14 * 24 * time.Hour,
		CatalogBaseURL: "https://cdn.example.test/catalogs",
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	req := definition.PublishRequest{
		Catalogs: []definition.CatalogSubmission{
			{
				CatalogID:   "example.test/CAT-1",
				SchemaTypes: []string{"retail"},
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
	if result.IndexVersion != 1 {
		t.Errorf("IndexVersion = %d, want 1", result.IndexVersion)
	}
	if len(result.Manifest) == 0 || len(result.Index) == 0 {
		t.Fatal("expected non-empty manifest and index")
	}

	var manifest dediManifest
	if err := json.Unmarshal(result.Manifest, &manifest); err != nil {
		t.Fatalf("parsing manifest: %v", err)
	}
	if manifest.DediVersion != dediVersion || manifest.Type != "dedi-manifest" {
		t.Errorf("unexpected manifest dedi_version/type: %+v", manifest)
	}
	if manifest.Domain != "example.test" {
		t.Errorf("manifest.Domain = %q, want example.test", manifest.Domain)
	}
	if manifest.UpdatedAt == nil {
		t.Error("expected manifest.updated_at to be set")
	}
	if manifest.NextUpdate == nil || !manifest.NextUpdate.After(*manifest.UpdatedAt) {
		t.Errorf("expected manifest.next_update to be after updated_at, got %+v", manifest.NextUpdate)
	}
	if len(manifest.Keys) != 1 || manifest.Keys[0].KID != "publisher-key-1" {
		t.Fatalf("unexpected manifest keys: %+v", manifest.Keys)
	}
	if manifest.Proof == nil || manifest.Proof.Jws == "" {
		t.Fatal("expected manifest to carry a proof")
	}
	if manifest.Proof.Canonicalization != "JCS" {
		t.Errorf("manifest.Proof.Canonicalization = %q, want JCS", manifest.Proof.Canonicalization)
	}
	if len(manifest.Files) != 1 || manifest.Files[0].Name != catalogIndexFileName {
		t.Fatalf("unexpected manifest files: %+v", manifest.Files)
	}
	if manifest.Files[0].Schema != "https://example.test/schemas/catalog-index.json" {
		t.Errorf("manifest.Files[0].Schema = %q, want the configured schema URL", manifest.Files[0].Schema)
	}

	var index catalogIndexDoc
	if err := json.Unmarshal(result.Index, &index); err != nil {
		t.Fatalf("parsing index: %v", err)
	}
	if index.ParticipantID != "example.test" {
		t.Errorf("index.ParticipantID = %q, want example.test", index.ParticipantID)
	}
	if index.Version != 1 {
		t.Errorf("index.Version = %d, want 1", index.Version)
	}
	if index.NextUpdate == nil {
		t.Error("expected index.next_update to be set")
	}
	if len(index.Catalogs) != 1 {
		t.Fatalf("expected 1 catalog entry, got %d", len(index.Catalogs))
	}

	var entry catalogEntry
	if err := json.Unmarshal(index.Catalogs[0], &entry); err != nil {
		t.Fatalf("parsing catalog entry: %v", err)
	}
	if entry.CatalogID != "example.test/CAT-1" || entry.Status != "ACTIVE" || entry.CatalogType != "REGULAR" {
		t.Fatalf("unexpected catalog entry: %+v", entry)
	}
	if entry.Baseline == nil {
		t.Fatal("expected a baseline file entry")
	}
	if entry.Baseline.Version != 1 {
		t.Errorf("baseline.Version = %d, want 1", entry.Baseline.Version)
	}
	if entry.Baseline.URL != "https://cdn.example.test/catalogs/CAT-1.v1.json" {
		t.Errorf("unexpected baseline URL: %q", entry.Baseline.URL)
	}
	wantDigest := "sha-256:" + digestOf(validCatalogJSON("CAT-1"))
	if entry.Baseline.Digest != wantDigest {
		t.Errorf("baseline.Digest = %q, want %q", entry.Baseline.Digest, wantDigest)
	}
	if entry.Baseline.Size != int64(len(validCatalogJSON("CAT-1"))) {
		t.Errorf("baseline.Size = %d, want %d", entry.Baseline.Size, len(validCatalogJSON("CAT-1")))
	}
	if entry.Baseline.Signature.KeyID != "publisher-key-1" || entry.Baseline.Signature.Value == "" {
		t.Errorf("unexpected baseline signature: %+v", entry.Baseline.Signature)
	}
	if len(entry.Changes) != 0 {
		t.Errorf("expected no changes on a fresh baseline, got %+v", entry.Changes)
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
	// A partial failure must still produce a valid manifest/index.
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

func TestCatalogPartURL_PlaceholderWhenUnconfigured(t *testing.T) {
	p := &Publisher{config: &Config{}}
	if got := p.catalogPartURL("CAT-1.v1.json"); got != "pending-artifact-store://catalog/CAT-1.v1.json" {
		t.Errorf("unexpected placeholder URL: %q", got)
	}
	p.config.CatalogBaseURL = "https://cdn.example.com/catalogs/"
	if got := p.catalogPartURL("CAT-1.v1.json"); got != "https://cdn.example.com/catalogs/CAT-1.v1.json" {
		t.Errorf("unexpected configured URL: %q", got)
	}
}

func TestLocalCatalogName(t *testing.T) {
	if got := localCatalogName("open-economy.nfh.global/electronics-2026"); got != "electronics-2026" {
		t.Errorf("localCatalogName = %q, want electronics-2026", got)
	}
	if got := localCatalogName("CAT-1"); got != "CAT-1" {
		t.Errorf("localCatalogName = %q, want CAT-1", got)
	}
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
		Catalog:      catalog,
		BaselineFile: &definition.FileRef{Version: 1, URL: "file://baseline.json", Digest: "sha-256:abc"},
	}

	result, err := p.Publish(context.Background(), definition.PublishRequest{
		Catalogs:          []definition.CatalogSubmission{{CatalogID: "CAT-1", Catalog: catalog}},
		PriorState:        map[string]definition.PriorCatalogState{"CAT-1": prior},
		PriorIndexVersion: 1,
	})
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}
	got := result.Catalogs[0]
	if got.Mode != "unchanged" || got.Changed || got.Version != 1 || got.Content != nil {
		t.Errorf("expected a no-op outcome, got %+v", got)
	}
	if result.IndexVersion != 1 {
		t.Errorf("expected index version to stay 1 on a total no-op, got %d", result.IndexVersion)
	}

	var index catalogIndexDoc
	if err := json.Unmarshal(result.Index, &index); err != nil {
		t.Fatalf("parsing index: %v", err)
	}
	var entry catalogEntry
	if err := json.Unmarshal(index.Catalogs[0], &entry); err != nil {
		t.Fatalf("parsing catalog entry: %v", err)
	}
	if len(entry.Changes) != 0 || entry.Baseline.URL != "file://baseline.json" {
		t.Errorf("unexpected index entry carried forward: %+v", entry)
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
		Catalog:      priorCatalog,
		BaselineFile: &definition.FileRef{Version: 1, URL: "https://cdn.test/catalogs/CAT-1.v1.json", Digest: "sha-256:abc"},
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
	if change.CatalogID != "CAT-1" || change.FromVersion != 1 || change.ToVersion != 2 {
		t.Errorf("unexpected change file header: %+v", change)
	}
	if len(change.Resources.Upserts) != 2 || len(change.Resources.Removals) != 1 {
		t.Fatalf("unexpected change contents: %+v", change.Resources)
	}
	if change.Resources.Removals[0] != "ITEM-2" {
		t.Errorf("Removals = %v, want [ITEM-2]", change.Resources.Removals)
	}
	upsertIDs := map[string]bool{}
	for _, u := range change.Resources.Upserts {
		id, _ := itemID(u)
		upsertIDs[id] = true
	}
	if !upsertIDs["ITEM-1"] || !upsertIDs["ITEM-3"] {
		t.Errorf("expected upserts for ITEM-1 (updated) and ITEM-3 (added), got %+v", upsertIDs)
	}

	var index catalogIndexDoc
	if err := json.Unmarshal(result.Index, &index); err != nil {
		t.Fatalf("parsing index: %v", err)
	}
	var entry catalogEntry
	if err := json.Unmarshal(index.Catalogs[0], &entry); err != nil {
		t.Fatalf("parsing catalog entry: %v", err)
	}
	if entry.Baseline.URL != "https://cdn.test/catalogs/CAT-1.v1.json" {
		t.Errorf("expected baseline carried forward unchanged, got %+v", entry.Baseline)
	}
	if len(entry.Changes) != 1 || entry.Changes[0].Version != 2 || entry.Changes[0].URL != "https://cdn.test/catalogs/CAT-1.v2.changes.json" {
		t.Errorf("unexpected change entry: %+v", entry.Changes)
	}
}

func TestPublish_Retire_ProducesTombstone(t *testing.T) {
	km := newFakeKeyManager(t, "k1")
	p, _, err := New(context.Background(), km, &Config{KeyID: "k1"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	result, err := p.Publish(context.Background(), definition.PublishRequest{
		Retire: []string{"CAT-OLD"},
	})
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}

	var index catalogIndexDoc
	if err := json.Unmarshal(result.Index, &index); err != nil {
		t.Fatalf("parsing index: %v", err)
	}
	if len(index.Catalogs) != 1 {
		t.Fatalf("expected 1 tombstone entry, got %d", len(index.Catalogs))
	}
	var entry catalogEntry
	if err := json.Unmarshal(index.Catalogs[0], &entry); err != nil {
		t.Fatalf("parsing entry: %v", err)
	}
	if entry.CatalogID != "CAT-OLD" || entry.Status != "RETIRED" || entry.RetiredAt == nil {
		t.Errorf("unexpected tombstone: %+v", entry)
	}
	if entry.Baseline != nil || len(entry.Changes) != 0 {
		t.Errorf("expected no files on a tombstone, got %+v", entry)
	}
}

func TestPublish_CarryForward_IncludedVerbatim(t *testing.T) {
	km := newFakeKeyManager(t, "k1")
	p, _, err := New(context.Background(), km, &Config{KeyID: "k1"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	other := json.RawMessage(`{"catalogId":"example.test/OTHER","status":"ACTIVE"}`)
	result, err := p.Publish(context.Background(), definition.PublishRequest{
		Catalogs:     []definition.CatalogSubmission{{CatalogID: "CAT-1", Catalog: validCatalogJSON("CAT-1")}},
		CarryForward: []json.RawMessage{other},
	})
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}

	var index catalogIndexDoc
	if err := json.Unmarshal(result.Index, &index); err != nil {
		t.Fatalf("parsing index: %v", err)
	}
	if len(index.Catalogs) != 2 {
		t.Fatalf("expected 2 entries (published + carried forward), got %d", len(index.Catalogs))
	}
	found := false
	for _, raw := range index.Catalogs {
		if string(raw) == string(other) {
			found = true
		}
	}
	if !found {
		t.Errorf("expected carried-forward entry to appear verbatim in %+v", index.Catalogs)
	}
}

func TestDiffCatalogs(t *testing.T) {
	prior := mustCatalogWithItems("CAT-1", "ITEM-1", "ITEM-2")
	next := json.RawMessage(`{"resources":[{"id":"ITEM-1","descriptor":{"name":"ITEM-1"}},{"id":"ITEM-3","descriptor":{"name":"ITEM-3"}}]}`)

	diff, changeCatalog, err := diffCatalogs(prior, next)
	if err != nil {
		t.Fatalf("diffCatalogs: %v", err)
	}
	if len(diff.Resources.Upserts) != 1 || len(diff.Resources.Removals) != 1 {
		t.Fatalf("unexpected diff: %+v", diff.Resources)
	}
	if diff.Resources.Removals[0] != "ITEM-2" {
		t.Errorf("Removals = %v, want [ITEM-2]", diff.Resources.Removals)
	}
	if changeCatalog != nil {
		t.Errorf("expected no catalog-level attribute change, got %s", changeCatalog)
	}
}

func TestDiffCatalogs_NoChangesIsEmpty(t *testing.T) {
	catalog := mustCatalogWithItems("CAT-1", "ITEM-1")
	diff, changeCatalog, err := diffCatalogs(catalog, catalog)
	if err != nil {
		t.Fatalf("diffCatalogs: %v", err)
	}
	if !diff.Resources.isEmpty() || !diff.Offers.isEmpty() {
		t.Errorf("expected empty diff for identical catalogs, got %+v", diff)
	}
	if changeCatalog != nil {
		t.Errorf("expected no catalog-level attribute change, got %s", changeCatalog)
	}
}

func TestDiffCatalogs_DescriptorChangeReportedUnderCatalog(t *testing.T) {
	prior := json.RawMessage(`{"id":"CAT-1","descriptor":{"name":"Old Name"},"provider":{},"resources":[]}`)
	next := json.RawMessage(`{"id":"CAT-1","descriptor":{"name":"New Name"},"provider":{},"resources":[]}`)

	diff, changeCatalog, err := diffCatalogs(prior, next)
	if err != nil {
		t.Fatalf("diffCatalogs: %v", err)
	}
	if !diff.Resources.isEmpty() {
		t.Errorf("expected no resource diff, got %+v", diff.Resources)
	}
	if changeCatalog == nil {
		t.Fatal("expected a catalog-level attribute change")
	}
	var attrs map[string]json.RawMessage
	if err := json.Unmarshal(changeCatalog, &attrs); err != nil {
		t.Fatalf("parsing changeCatalog: %v", err)
	}
	if _, ok := attrs["descriptor"]; !ok {
		t.Errorf("expected descriptor in changeCatalog, got %s", changeCatalog)
	}
}

func TestPublish_ExtraManifestFiles_AppendedVerbatimNoDigest(t *testing.T) {
	km := newFakeKeyManager(t, "k1")
	p, _, err := New(context.Background(), km, &Config{
		KeyID: "k1",
		ExtraManifestFiles: []ManifestFileRef{
			{Name: "beckn-subscriber", URL: "https://example.test/dedi/beckn-subscriber.dedi.json", Schema: "https://example.test/schemas/Beckn_subscriber.json"},
		},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	result, err := p.Publish(context.Background(), definition.PublishRequest{
		Catalogs: []definition.CatalogSubmission{{CatalogID: "CAT-1", Catalog: validCatalogJSON("CAT-1")}},
	})
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}

	var manifest dediManifest
	if err := json.Unmarshal(result.Manifest, &manifest); err != nil {
		t.Fatalf("parsing manifest: %v", err)
	}
	if len(manifest.Files) != 2 {
		t.Fatalf("expected 2 manifest files (catalog-index + extra), got %d: %+v", len(manifest.Files), manifest.Files)
	}
	extra := manifest.Files[1]
	if extra.Name != "beckn-subscriber" || extra.URL != "https://example.test/dedi/beckn-subscriber.dedi.json" || extra.Schema != "https://example.test/schemas/Beckn_subscriber.json" {
		t.Errorf("extra manifest file not passed through verbatim: %+v", extra)
	}
}
