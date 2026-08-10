package catalogpublisher

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/beckn-one/beckn-onix/pkg/model"
	"github.com/beckn-one/beckn-onix/pkg/plugin/definition"
)

// fakeKeyManager returns a fixed Ed25519 keyset for one configured
// subscriberID (the lookup key every Keyset caller uses); it satisfies
// definition.KeyManager but only Keyset is ever exercised here. keyID and
// domain populate the returned Keyset's UniqueKeyID/SubscriberID -- the
// same fields catalogpublisher now derives its JWK kid and manifest domain
// from, instead of duplicating them in its own Config.
type fakeKeyManager struct {
	keyID  string // also doubles as the lookup key (subscriberID) in these tests
	domain string
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
		SubscriberID:   f.domain,
		UniqueKeyID:    f.keyID,
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

	if _, _, err := New(context.Background(), nil, &Config{SubscriberID: "k1"}); err == nil {
		t.Fatal("expected error for nil KeyManager")
	}
	if _, _, err := New(context.Background(), km, &Config{}); err == nil {
		t.Fatal("expected error for missing keyID")
	}
	if _, _, err := New(context.Background(), km, &Config{SubscriberID: "k1"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestPublish_SingleCatalog_ProducesIndex(t *testing.T) {
	km := newFakeKeyManager(t, "publisher-key-1")
	km.domain = "example.test"
	p, _, err := New(context.Background(), km, &Config{
		SubscriberID:  "publisher-key-1",
		NextUpdateIn:  14 * 24 * time.Hour,
		PublicBaseURL: "https://cdn.example.test",
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
	if len(result.Catalogs) != 1 || !result.Catalogs[0].Changed || result.Catalogs[0].Version != 1 || result.Catalogs[0].EntryVersion != 1 {
		t.Fatalf("unexpected catalog outcomes: %+v", result.Catalogs)
	}
	if len(result.Index) == 0 {
		t.Fatal("expected non-empty index")
	}

	var index catalogIndexDoc
	if err := json.Unmarshal(result.Index, &index); err != nil {
		t.Fatalf("parsing index: %v", err)
	}
	if index.NodeID != "example.test" {
		t.Errorf("index.NodeID = %q, want example.test", index.NodeID)
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
	if entry.CatalogID != "example.test/CAT-1" || entry.CatalogType != "REGULAR" {
		t.Fatalf("unexpected catalog entry: %+v", entry)
	}
	if entry.EntryVersion != 1 {
		t.Errorf("entry.EntryVersion = %d, want 1", entry.EntryVersion)
	}
	if entry.IsActive == nil || !*entry.IsActive {
		t.Errorf("expected entry.IsActive = true (default), got %+v", entry.IsActive)
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
	if len(entry.Changes) != 0 {
		t.Errorf("expected no changes on a fresh baseline, got %+v", entry.Changes)
	}
	if entry.Signature.KeyID != "publisher-key-1" || entry.Signature.Value == "" {
		t.Errorf("unexpected catalog-entry signature: %+v", entry.Signature)
	}

	// The baseline file itself is self-signed (file spec v2): its published
	// content wraps the submitted catalog with its own {keyId,
	// canonicalization, value} signature, and digest/size are computed over
	// that final, already-signed content -- not the bare submitted catalog.
	content := result.Catalogs[0].Content
	wantDigest := "sha-256:" + digestOf(content)
	if entry.Baseline.Digest != wantDigest {
		t.Errorf("baseline.Digest = %q, want sha-256 of the actual published (signed) content %q", entry.Baseline.Digest, wantDigest)
	}
	if entry.Baseline.Size != int64(len(content)) {
		t.Errorf("baseline.Size = %d, want %d", entry.Baseline.Size, len(content))
	}
	var file catalogFileDoc
	if err := json.Unmarshal(content, &file); err != nil {
		t.Fatalf("parsing baseline file content: %v", err)
	}
	if string(file.Catalog) != string(validCatalogJSON("CAT-1")) {
		t.Errorf("baseline file's wrapped catalog = %s, want %s", file.Catalog, validCatalogJSON("CAT-1"))
	}
	if file.Signature.KeyID != "publisher-key-1" || file.Signature.Canonicalization != "JCS" || file.Signature.Value == "" {
		t.Errorf("unexpected baseline file self-signature: %+v", file.Signature)
	}
	if file.CatalogID == "" || file.Version != 1 || file.NextUpdate.IsZero() {
		t.Errorf("baseline file missing required identity/freshness fields: %+v", file)
	}
}

// TestPublish_PublishLatest_AddsOverwrittenPointer proves the NFH-014
// "latest" pointer: when Config.PublishLatest is on, every publishOne call
// -- including ones that don't produce a new baseline/change file --
// regenerates a full, self-signed CatalogFile at a fixed, non-versioned
// URL, and stamps entry.Latest.Version with the catalog's current
// content-lineage version.
func TestPublish_PublishLatest_AddsOverwrittenPointer(t *testing.T) {
	km := newFakeKeyManager(t, "k1")
	p, _, err := New(context.Background(), km, &Config{SubscriberID: "k1", PublicBaseURL: "https://cdn.test", PublishLatest: true})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	result, err := p.Publish(context.Background(), definition.PublishRequest{
		Catalogs: []definition.CatalogSubmission{{CatalogID: "CAT-1", Catalog: validCatalogJSON("CAT-1")}},
	})
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}
	got := result.Catalogs[0]
	if got.LatestContent == nil || got.LatestDigest == "" {
		t.Fatalf("expected LatestContent/LatestDigest to be set, got %+v", got)
	}

	var index catalogIndexDoc
	if err := json.Unmarshal(result.Index, &index); err != nil {
		t.Fatalf("parsing index: %v", err)
	}
	var entry catalogEntry
	if err := json.Unmarshal(index.Catalogs[0], &entry); err != nil {
		t.Fatalf("parsing entry: %v", err)
	}
	if entry.Latest == nil || entry.Latest.Version != 1 {
		t.Fatalf("expected entry.Latest at version 1, got %+v", entry.Latest)
	}
	if entry.Latest.URL != "https://cdn.test/catalogs/CAT-1.latest.json" {
		t.Errorf("unexpected latest URL: %q", entry.Latest.URL)
	}
	if entry.Latest.Digest != got.LatestDigest {
		t.Errorf("entry.Latest.Digest = %q, want %q", entry.Latest.Digest, got.LatestDigest)
	}

	var file catalogFileDoc
	if err := json.Unmarshal(got.LatestContent, &file); err != nil {
		t.Fatalf("parsing latest content: %v", err)
	}
	if file.Signature.Value == "" {
		t.Error("expected latest content to carry a self-signature")
	}
}

// TestPublish_PublishLatestDisabled_OmitsPointer proves latest is opt-in.
func TestPublish_PublishLatestDisabled_OmitsPointer(t *testing.T) {
	km := newFakeKeyManager(t, "k1")
	p, _, err := New(context.Background(), km, &Config{SubscriberID: "k1"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	result, err := p.Publish(context.Background(), definition.PublishRequest{
		Catalogs: []definition.CatalogSubmission{{CatalogID: "CAT-1", Catalog: validCatalogJSON("CAT-1")}},
	})
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if result.Catalogs[0].LatestContent != nil {
		t.Error("expected no LatestContent when PublishLatest is off")
	}
	var index catalogIndexDoc
	if err := json.Unmarshal(result.Index, &index); err != nil {
		t.Fatalf("parsing index: %v", err)
	}
	var entry catalogEntry
	if err := json.Unmarshal(index.Catalogs[0], &entry); err != nil {
		t.Fatalf("parsing entry: %v", err)
	}
	if entry.Latest != nil {
		t.Errorf("expected no latest field, got %+v", entry.Latest)
	}
}

// TestPublish_Gzip_CompressesServedContentAndURL proves NFH-014 §10.1
// compression: when Config.Gzip is on, the baseline's URL/filename carry
// a ".json.gz" extension, ServedContent is gzip-compressed relative to
// Content, and -- critically -- the digest and Content itself stay the
// canonical, decompressed bytes (CON-TBD-29: never compute digest/
// signature against compressed bytes).
func TestPublish_Gzip_CompressesServedContentAndURL(t *testing.T) {
	km := newFakeKeyManager(t, "k1")
	p, _, err := New(context.Background(), km, &Config{SubscriberID: "k1", PublicBaseURL: "https://cdn.test", Gzip: true})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	result, err := p.Publish(context.Background(), definition.PublishRequest{
		Catalogs: []definition.CatalogSubmission{{CatalogID: "CAT-1", Catalog: validCatalogJSON("CAT-1")}},
	})
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}
	got := result.Catalogs[0]
	if !got.Compressed {
		t.Fatal("expected Compressed = true")
	}
	if got.ServedContent == nil || string(got.ServedContent) == string(got.Content) {
		t.Fatalf("expected ServedContent to be compressed and differ from Content")
	}
	gr, err := gzip.NewReader(bytes.NewReader(got.ServedContent))
	if err != nil {
		t.Fatalf("ServedContent is not valid gzip: %v", err)
	}
	decompressed, err := io.ReadAll(gr)
	if err != nil {
		t.Fatalf("decompressing ServedContent: %v", err)
	}
	if string(decompressed) != string(got.Content) {
		t.Errorf("decompressed ServedContent = %s, want %s", decompressed, got.Content)
	}
	wantDigest := "sha-256:" + digestOf(got.Content)
	if got.Digest != wantDigest {
		t.Errorf("Digest = %q, want sha-256 of the canonical (decompressed) content %q", got.Digest, wantDigest)
	}

	var index catalogIndexDoc
	if err := json.Unmarshal(result.Index, &index); err != nil {
		t.Fatalf("parsing index: %v", err)
	}
	var entry catalogEntry
	if err := json.Unmarshal(index.Catalogs[0], &entry); err != nil {
		t.Fatalf("parsing entry: %v", err)
	}
	if entry.Baseline == nil || !strings.HasSuffix(entry.Baseline.URL, ".json.gz") {
		t.Errorf("expected baseline URL to end in .json.gz, got %+v", entry.Baseline)
	}
	if entry.Baseline.Size != int64(len(got.ServedContent)) {
		t.Errorf("baseline.Size = %d, want the compressed served size %d", entry.Baseline.Size, len(got.ServedContent))
	}
}

func TestPublish_InvalidSubmissionIsNonFatal(t *testing.T) {
	km := newFakeKeyManager(t, "k1")
	p, _, err := New(context.Background(), km, &Config{SubscriberID: "k1"})
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
	// A partial failure must still produce a valid index.
	if len(result.Index) == 0 {
		t.Fatal("expected index to still be produced despite partial failure")
	}
}

func TestPublish_UnknownKeyIDFails(t *testing.T) {
	km := newFakeKeyManager(t, "k1")
	p, _, err := New(context.Background(), km, &Config{SubscriberID: "wrong-key"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := p.Publish(context.Background(), definition.PublishRequest{}); err == nil {
		t.Fatal("expected error for unknown keyID")
	}
}

func TestCatalogPartURL_PlaceholderWhenUnconfigured(t *testing.T) {
	p := &Publisher{config: &Config{}}
	if got := p.catalogPartURL("CAT-1.v1.json", "json"); got != "pending-artifact-store://catalog/CAT-1.v1.json" {
		t.Errorf("unexpected placeholder URL: %q", got)
	}
	p.config.PublicBaseURL = "https://cdn.example.com/"
	if got := p.catalogPartURL("CAT-1.v1.json", "json"); got != "https://cdn.example.com/catalogs/CAT-1.v1.json" {
		t.Errorf("unexpected configured baseline URL: %q", got)
	}
	if got := p.catalogPartURL("CAT-1.v2.changes.json", "changes.json"); got != "https://cdn.example.com/catalogs/changes/CAT-1.v2.changes.json" {
		t.Errorf("unexpected configured change-file URL: %q", got)
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
	p, _, err := New(context.Background(), km, &Config{SubscriberID: "k1"})
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
	var file catalogFileDoc
	if err := json.Unmarshal(got.Content, &file); err != nil {
		t.Fatalf("parsing baseline content: %v", err)
	}
	if string(file.Catalog) != string(mustCatalogWithItems("CAT-1", "ITEM-1", "ITEM-2")) {
		t.Errorf("expected baseline content to wrap the submitted catalog, got %s", file.Catalog)
	}
	if file.Signature.Value == "" {
		t.Error("expected baseline content to carry a self-signature")
	}
}

func TestPublish_Incremental_UnchangedProducesNoOp(t *testing.T) {
	km := newFakeKeyManager(t, "k1")
	p, _, err := New(context.Background(), km, &Config{SubscriberID: "k1"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	catalog := mustCatalogWithItems("CAT-1", "ITEM-1", "ITEM-2")
	prior := definition.PriorCatalogState{
		Catalog:      catalog,
		BaselineFile: &definition.FileRef{Version: 1, URL: "file://baseline.json", Digest: "sha-256:abc"},
		EntryVersion: 1,
		CatalogType:  "REGULAR",
		IsActive:     true,
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
	if got.EntryVersion != prior.EntryVersion {
		t.Errorf("expected entryVersion to stay %d on a total no-op, got %d", prior.EntryVersion, got.EntryVersion)
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
	p, _, err := New(context.Background(), km, &Config{SubscriberID: "k1", PublicBaseURL: "https://cdn.test"})
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
	if change.NextUpdate.IsZero() {
		t.Error("expected change file's next_update to be set")
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
	if len(entry.Changes) != 1 || entry.Changes[0].Version != 2 || entry.Changes[0].URL != "https://cdn.test/catalogs/changes/CAT-1.v2.changes.json" {
		t.Errorf("unexpected change entry: %+v", entry.Changes)
	}
}

// TestPublish_MetadataOnlyChange_BumpsEntryVersionWithoutNewFile proves the
// NFH-014 §Versioning fix: editing only NetworkIds (no resource/offer/
// catalog-attribute change) must still bump EntryVersion and re-sign the
// entry, without producing a new baseline/change file or touching the
// file-lineage Version.
func TestPublish_MetadataOnlyChange_BumpsEntryVersionWithoutNewFile(t *testing.T) {
	km := newFakeKeyManager(t, "k1")
	p, _, err := New(context.Background(), km, &Config{SubscriberID: "k1"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	catalog := mustCatalogWithItems("CAT-1", "ITEM-1")
	prior := definition.PriorCatalogState{
		Catalog:      catalog,
		BaselineFile: &definition.FileRef{Version: 1, URL: "file://baseline.json", Digest: "sha-256:abc"},
		EntryVersion: 3,
		CatalogType:  "REGULAR",
		IsActive:     true,
		NetworkIds:   []string{"old.network"},
	}

	result, err := p.Publish(context.Background(), definition.PublishRequest{
		Catalogs:   []definition.CatalogSubmission{{CatalogID: "CAT-1", Catalog: catalog, NetworkIds: []string{"new.network"}}},
		PriorState: map[string]definition.PriorCatalogState{"CAT-1": prior},
	})
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}
	got := result.Catalogs[0]
	if got.Mode != "metadata" || !got.Changed || got.Content != nil || got.Version != 1 {
		t.Fatalf("expected a metadata-only outcome, got %+v", got)
	}
	if got.EntryVersion != 4 {
		t.Errorf("EntryVersion = %d, want 4 (bumped from prior 3)", got.EntryVersion)
	}

	var index catalogIndexDoc
	if err := json.Unmarshal(result.Index, &index); err != nil {
		t.Fatalf("parsing index: %v", err)
	}
	var entry catalogEntry
	if err := json.Unmarshal(index.Catalogs[0], &entry); err != nil {
		t.Fatalf("parsing entry: %v", err)
	}
	if len(entry.NetworkIds) != 1 || entry.NetworkIds[0] != "new.network" {
		t.Errorf("expected the new NetworkIds in the re-signed entry, got %+v", entry.NetworkIds)
	}
	if entry.Baseline == nil || entry.Baseline.Version != 1 {
		t.Errorf("expected the baseline file reference to stay unchanged, got %+v", entry.Baseline)
	}
}

// TestPublish_ForceBaseline_KeepsPriorChangesListed proves the compaction
// grace-period fix (NFH-014 CON-TBD-32): a forced re-baseline must not
// reset Changes to nil -- the pre-compaction change files stay listed
// (not just hosted) so a DS mid-lineage can still reach equivalent content
// by applying them. How long to keep passing them is the caller's own
// policy (PriorCatalogState's doc comment); Publish itself just doesn't
// discard what it's handed.
func TestPublish_ForceBaseline_KeepsPriorChangesListed(t *testing.T) {
	km := newFakeKeyManager(t, "k1")
	p, _, err := New(context.Background(), km, &Config{SubscriberID: "k1"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	catalog := mustCatalogWithItems("CAT-1", "ITEM-1")
	priorChange := definition.FileRef{Version: 2, URL: "file://v2.changes.json", Digest: "sha-256:def"}
	prior := definition.PriorCatalogState{
		Catalog:      catalog,
		BaselineFile: &definition.FileRef{Version: 1, URL: "file://v1.json", Digest: "sha-256:abc"},
		ChangeFiles:  []definition.FileRef{priorChange},
		EntryVersion: 2,
		CatalogType:  "REGULAR",
	}

	result, err := p.Publish(context.Background(), definition.PublishRequest{
		Catalogs:      []definition.CatalogSubmission{{CatalogID: "CAT-1", Catalog: catalog}},
		PriorState:    map[string]definition.PriorCatalogState{"CAT-1": prior},
		ForceBaseline: true,
	})
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}
	got := result.Catalogs[0]
	if got.Mode != "baseline" || got.Version != 3 {
		t.Fatalf("unexpected outcome: %+v", got)
	}

	var index catalogIndexDoc
	if err := json.Unmarshal(result.Index, &index); err != nil {
		t.Fatalf("parsing index: %v", err)
	}
	var entry catalogEntry
	if err := json.Unmarshal(index.Catalogs[0], &entry); err != nil {
		t.Fatalf("parsing entry: %v", err)
	}
	if entry.Baseline == nil || entry.Baseline.Version != 3 {
		t.Fatalf("expected the new baseline at version 3, got %+v", entry.Baseline)
	}
	if len(entry.Changes) != 1 || entry.Changes[0].Version != 2 || entry.Changes[0].URL != priorChange.URL {
		t.Errorf("expected the pre-compaction change file to stay listed, got %+v", entry.Changes)
	}
}

func TestPublish_Retire_ProducesTombstone(t *testing.T) {
	km := newFakeKeyManager(t, "k1")
	p, _, err := New(context.Background(), km, &Config{SubscriberID: "k1"})
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
	if entry.CatalogID != "CAT-OLD" || entry.RetiredAt == nil || entry.EntryVersion != 1 {
		t.Errorf("unexpected tombstone: %+v", entry)
	}
	if entry.Baseline != nil || len(entry.Changes) != 0 || entry.IsActive != nil {
		t.Errorf("expected no files/isActive on a tombstone, got %+v", entry)
	}
}

// TestPublish_RetireWithPriorState_KeepsMetadataAndBumpsEntryVersion
// proves NFH-014 Appendix A Example 4's retired entry shape: catalogType/
// networkIds/schemaTypes survive retirement (only isActive/baseline/
// changes are dropped), and EntryVersion continues its lineage from the
// catalog's prior EntryVersion rather than resetting to 1.
func TestPublish_RetireWithPriorState_KeepsMetadataAndBumpsEntryVersion(t *testing.T) {
	km := newFakeKeyManager(t, "k1")
	p, _, err := New(context.Background(), km, &Config{SubscriberID: "k1"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	result, err := p.Publish(context.Background(), definition.PublishRequest{
		Retire: []string{"CAT-OLD"},
		PriorState: map[string]definition.PriorCatalogState{
			"CAT-OLD": {
				EntryVersion: 5,
				CatalogType:  "REGULAR",
				NetworkIds:   []string{"ion.example"},
				SchemaTypes:  []string{"https://schema.example/1.0.0/context.jsonld"},
			},
		},
	})
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}

	var index catalogIndexDoc
	if err := json.Unmarshal(result.Index, &index); err != nil {
		t.Fatalf("parsing index: %v", err)
	}
	var entry catalogEntry
	if err := json.Unmarshal(index.Catalogs[0], &entry); err != nil {
		t.Fatalf("parsing entry: %v", err)
	}
	if entry.EntryVersion != 6 {
		t.Errorf("EntryVersion = %d, want 6 (bumped from prior 5)", entry.EntryVersion)
	}
	if entry.CatalogType != "REGULAR" || len(entry.NetworkIds) != 1 || len(entry.SchemaTypes) != 1 {
		t.Errorf("expected prior metadata to survive retirement, got %+v", entry)
	}
	if entry.IsActive != nil || entry.Baseline != nil || len(entry.Changes) != 0 {
		t.Errorf("expected isActive/baseline/changes dropped on retirement, got %+v", entry)
	}
}

func TestPublish_CarryForward_IncludedVerbatim(t *testing.T) {
	km := newFakeKeyManager(t, "k1")
	p, _, err := New(context.Background(), km, &Config{SubscriberID: "k1"})
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

func TestDiffCatalogs_DuplicateSubmittedIDsCollapseToOneUpsert(t *testing.T) {
	prior := mustCatalogWithItems("CAT-1")
	next := json.RawMessage(`{"resources":[{"id":"ITEM-1","descriptor":{"name":"v1"}},{"id":"ITEM-1","descriptor":{"name":"v2"}}]}`)

	diff, _, err := diffCatalogs(prior, next)
	if err != nil {
		t.Fatalf("diffCatalogs: %v", err)
	}
	if len(diff.Resources.Upserts) != 1 {
		t.Fatalf("expected exactly 1 upsert for a duplicated id, got %d: %+v", len(diff.Resources.Upserts), diff.Resources.Upserts)
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

// TestDiffCatalogs_ArbitraryAttributeChangeReportedUnderCatalog proves the
// catalog-level attribute overlay isn't limited to a hardcoded
// descriptor/provider field list -- any top-level field the submitted
// catalog changes (e.g. a validity window) is reported under changeCatalog.
func TestDiffCatalogs_ArbitraryAttributeChangeReportedUnderCatalog(t *testing.T) {
	prior := json.RawMessage(`{"id":"CAT-1","descriptor":{"name":"X"},"provider":{},"resources":[],"validity":{"startDate":"2026-01-01T00:00:00Z","endDate":"2026-06-30T23:59:59Z"}}`)
	next := json.RawMessage(`{"id":"CAT-1","descriptor":{"name":"X"},"provider":{},"resources":[],"validity":{"startDate":"2026-07-01T00:00:00Z","endDate":"2026-12-31T23:59:59Z"}}`)

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
	if _, ok := attrs["validity"]; !ok {
		t.Errorf("expected validity in changeCatalog, got %s", changeCatalog)
	}
	if _, ok := attrs["descriptor"]; ok {
		t.Errorf("expected no descriptor change reported, got %s", changeCatalog)
	}
}

func TestFileRefsToWire(t *testing.T) {
	if got := fileRefsToWire(nil); got != nil {
		t.Errorf("expected nil for no file refs, got %+v", got)
	}
	in := []definition.FileRef{{
		Version: 3, URL: "https://example.test/catalogs/CAT-1.v3.json", Size: 42, Digest: "sha-256:abc",
	}}
	got := fileRefsToWire(in)
	if len(got) != 1 || got[0].Version != 3 || got[0].URL != in[0].URL || got[0].Size != 42 || got[0].Digest != "sha-256:abc" {
		t.Errorf("fileRefsToWire did not round-trip fields, got %+v", got)
	}
}

func TestIndexURL(t *testing.T) {
	p := &Publisher{config: &Config{}}
	if got := p.IndexURL(); got != "pending-artifact-store://catalog-index.json" {
		t.Errorf("expected placeholder index URL when PublicBaseURL is unset, got %q", got)
	}
	p.config.PublicBaseURL = "https://example.test"
	if got := p.IndexURL(); got != "https://example.test/index/becknCatalogs.index.json" {
		t.Errorf("expected index URL under PublicBaseURL, got %q", got)
	}
}

func TestDecodeKeyset_NilKeysetIsError(t *testing.T) {
	if _, _, err := decodeKeyset(nil); err == nil {
		t.Fatal("expected error for nil keyset")
	}
}

func TestDecodeKeyset_InvalidBase64IsError(t *testing.T) {
	if _, _, err := decodeKeyset(&model.Keyset{SigningPrivate: "not-base64!!", SigningPublic: "AA=="}); err == nil {
		t.Fatal("expected error for invalid base64 signing private key")
	}
	validSeed := base64.StdEncoding.EncodeToString(make([]byte, ed25519.SeedSize))
	if _, _, err := decodeKeyset(&model.Keyset{SigningPrivate: validSeed, SigningPublic: "not-base64!!"}); err == nil {
		t.Fatal("expected error for invalid base64 signing public key")
	}
}

func TestDecodeKeyset_WrongKeyLengthIsError(t *testing.T) {
	shortSeed := base64.StdEncoding.EncodeToString([]byte("too-short"))
	if _, _, err := decodeKeyset(&model.Keyset{SigningPrivate: shortSeed, SigningPublic: base64.StdEncoding.EncodeToString(make([]byte, ed25519.PublicKeySize))}); err == nil {
		t.Fatal("expected error for wrong-length signing private key")
	}

	validSeed := base64.StdEncoding.EncodeToString(make([]byte, ed25519.SeedSize))
	shortPub := base64.StdEncoding.EncodeToString([]byte("too-short"))
	if _, _, err := decodeKeyset(&model.Keyset{SigningPrivate: validSeed, SigningPublic: shortPub}); err == nil {
		t.Fatal("expected error for wrong-length signing public key")
	}
}

func TestDecodeKeyset_Valid(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("generating key: %v", err)
	}
	keyset := &model.Keyset{
		SigningPrivate: base64.StdEncoding.EncodeToString(priv.Seed()),
		SigningPublic:  base64.StdEncoding.EncodeToString(pub),
	}
	gotPriv, gotPub, err := decodeKeyset(keyset)
	if err != nil {
		t.Fatalf("decodeKeyset: %v", err)
	}
	if !gotPriv.Equal(priv) || !gotPub.Equal(pub) {
		t.Error("decoded keys do not match input")
	}
}
