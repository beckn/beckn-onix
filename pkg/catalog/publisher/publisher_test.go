package publisher

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/ed25519"
	"encoding/json"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/beckn-one/beckn-onix/pkg/catalog"
	"github.com/beckn-one/beckn-onix/pkg/catalog/store"
)

func testSigningKey(t *testing.T) (ed25519.PrivateKey, string) {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("generating key: %v", err)
	}
	return priv, "test-key-1"
}

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

func TestPublish_SingleCatalog_ProducesIndex(t *testing.T) {
	priv, keyID := testSigningKey(t)
	result, err := Publish(context.Background(), Params{
		Catalogs: []Submission{{
			CatalogID:   "example.test/CAT-1",
			SchemaTypes: []string{"retail"},
			Catalog:     validCatalogJSON("CAT-1"),
		}},
		NextUpdateIn:  14 * 24 * time.Hour,
		PublicBaseURL: "https://cdn.example.test",
		SigningKey:    priv, KeyID: keyID, Domain: "example.test",
	})
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if len(result.Errors) != 0 {
		t.Fatalf("expected no errors, got %+v", result.Errors)
	}
	if len(result.Reports) != 1 || !result.Reports[0].Changed || result.Reports[0].Version != 1 || result.Reports[0].EntryVersion != 1 {
		t.Fatalf("unexpected reports: %+v", result.Reports)
	}
	if result.Publish.NodeID != "example.test" {
		t.Errorf("result.Publish.NodeID = %q, want example.test", result.Publish.NodeID)
	}
	if result.Publish.NextUpdate == nil {
		t.Error("expected result.Publish.NextUpdate to be set")
	}
	if len(result.Publish.Updates) != 1 || len(result.Publish.Updates[0].SignedEntry) == 0 {
		t.Fatal("expected a non-empty signed entry")
	}

	var entry catalog.CatalogEntry
	if err := json.Unmarshal(result.Publish.Updates[0].SignedEntry, &entry); err != nil {
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
	if entry.Baseline.URL == "" {
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
	if entry.Signature.KeyID != keyID || entry.Signature.Value == "" {
		t.Errorf("unexpected catalog-entry signature: %+v", entry.Signature)
	}

	// The baseline file itself is self-signed (file spec v2): its published
	// content wraps the submitted catalog with its own {keyId,
	// canonicalization, value} signature, and digest/size are computed over
	// that final, already-signed content -- not the bare submitted catalog.
	content := result.Publish.Updates[0].Baseline.Content
	wantDigest := "sha-256:" + digestOf(content)
	if entry.Baseline.Digest != wantDigest {
		t.Errorf("baseline.Digest = %q, want sha-256 of the actual published (signed) content %q", entry.Baseline.Digest, wantDigest)
	}
	if entry.Baseline.Size != int64(len(content)) {
		t.Errorf("baseline.Size = %d, want %d", entry.Baseline.Size, len(content))
	}
	var file catalog.CatalogFileDoc
	if err := json.Unmarshal(content, &file); err != nil {
		t.Fatalf("parsing baseline file content: %v", err)
	}
	if string(file.Catalog) != string(validCatalogJSON("CAT-1")) {
		t.Errorf("baseline file's wrapped catalog = %s, want %s", file.Catalog, validCatalogJSON("CAT-1"))
	}
	if file.Signature.KeyID != keyID || file.Signature.Canonicalization != "JCS" || file.Signature.Value == "" {
		t.Errorf("unexpected baseline file self-signature: %+v", file.Signature)
	}
	if file.CatalogID == "" || file.Version != 1 || file.NextUpdate.IsZero() {
		t.Errorf("baseline file missing required identity/freshness fields: %+v", file)
	}
}

// TestPublish_Dependencies_IncludesMasterVersion proves
// dependencies.masters[] carries the caller-supplied Version through to
// the wire (NFH-014: "the MASTER's baseline.version last validated
// against").
func TestPublish_Dependencies_IncludesMasterVersion(t *testing.T) {
	priv, keyID := testSigningKey(t)
	result, err := Publish(context.Background(), Params{
		Catalogs: []Submission{{
			CatalogID: "CAT-1", Catalog: validCatalogJSON("CAT-1"),
			Dependencies: []catalog.MasterDependency{
				{CatalogID: "CAT-MASTER", Version: 12, IndexURL: "https://cdn.test/catalog-index.json"},
			},
		}},
		SigningKey: priv, KeyID: keyID, Domain: "example.test",
	})
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}
	var entry catalog.CatalogEntry
	if err := json.Unmarshal(result.Publish.Updates[0].SignedEntry, &entry); err != nil {
		t.Fatalf("parsing entry: %v", err)
	}
	if entry.Dependencies == nil || len(entry.Dependencies.Masters) != 1 {
		t.Fatalf("expected 1 master dependency, got %+v", entry.Dependencies)
	}
	m := entry.Dependencies.Masters[0]
	if m.CatalogID != "CAT-MASTER" || m.Version != 12 || m.IndexURL != "https://cdn.test/catalog-index.json" {
		t.Errorf("unexpected master dependency: %+v", m)
	}
}

// TestPublish_PublishLatest_AddsOverwrittenPointer proves the NFH-014
// "latest" pointer: when PublishLatest is on, every publishOne call --
// including ones that don't produce a new baseline/change file --
// regenerates a full, self-signed CatalogFileDoc at a fixed,
// non-versioned URL, and stamps entry.Latest.Version with the catalog's
// current content-lineage version.
func TestPublish_PublishLatest_AddsOverwrittenPointer(t *testing.T) {
	priv, keyID := testSigningKey(t)
	result, err := Publish(context.Background(), Params{
		Catalogs:      []Submission{{CatalogID: "CAT-1", Catalog: validCatalogJSON("CAT-1")}},
		PublicBaseURL: "https://cdn.test", PublishLatest: true,
		SigningKey: priv, KeyID: keyID, Domain: "example.test",
	})
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}
	got := result.Reports[0]
	if got.LatestContent == nil || got.LatestDigest == "" {
		t.Fatalf("expected LatestContent/LatestDigest to be set, got %+v", got)
	}

	var entry catalog.CatalogEntry
	if err := json.Unmarshal(result.Publish.Updates[0].SignedEntry, &entry); err != nil {
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

	var file catalog.CatalogFileDoc
	if err := json.Unmarshal(got.LatestContent, &file); err != nil {
		t.Fatalf("parsing latest content: %v", err)
	}
	if file.Signature.Value == "" {
		t.Error("expected latest content to carry a self-signature")
	}
}

// TestPublish_PublishLatestDisabled_OmitsPointer proves latest is opt-in.
func TestPublish_PublishLatestDisabled_OmitsPointer(t *testing.T) {
	priv, keyID := testSigningKey(t)
	result, err := Publish(context.Background(), Params{
		Catalogs:   []Submission{{CatalogID: "CAT-1", Catalog: validCatalogJSON("CAT-1")}},
		SigningKey: priv, KeyID: keyID, Domain: "example.test",
	})
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if result.Reports[0].LatestContent != nil {
		t.Error("expected no LatestContent when PublishLatest is off")
	}
	var entry catalog.CatalogEntry
	if err := json.Unmarshal(result.Publish.Updates[0].SignedEntry, &entry); err != nil {
		t.Fatalf("parsing entry: %v", err)
	}
	if entry.Latest != nil {
		t.Errorf("expected no latest field, got %+v", entry.Latest)
	}
}

// TestPublish_Gzip_CompressesServedContentAndURL proves NFH-014 §10.1
// compression: when Gzip is on, the baseline's URL/filename carry a
// ".json.gz" extension, ServedContent is gzip-compressed relative to
// Content, and -- critically -- the digest and Content itself stay the
// canonical, decompressed bytes (CON-TBD-29: never compute digest/
// signature against compressed bytes).
func TestPublish_Gzip_CompressesServedContentAndURL(t *testing.T) {
	priv, keyID := testSigningKey(t)
	result, err := Publish(context.Background(), Params{
		Catalogs:      []Submission{{CatalogID: "CAT-1", Catalog: validCatalogJSON("CAT-1")}},
		PublicBaseURL: "https://cdn.test", Gzip: true,
		SigningKey: priv, KeyID: keyID, Domain: "example.test",
	})
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}
	upd := result.Publish.Updates[0]
	if !upd.Baseline.Compressed {
		t.Fatal("expected Compressed = true")
	}
	if upd.Baseline.ServedContent == nil || string(upd.Baseline.ServedContent) == string(upd.Baseline.Content) {
		t.Fatalf("expected ServedContent to be compressed and differ from Content")
	}
	gr, err := gzip.NewReader(bytes.NewReader(upd.Baseline.ServedContent))
	if err != nil {
		t.Fatalf("ServedContent is not valid gzip: %v", err)
	}
	decompressed, err := io.ReadAll(gr)
	if err != nil {
		t.Fatalf("decompressing ServedContent: %v", err)
	}
	if string(decompressed) != string(upd.Baseline.Content) {
		t.Errorf("decompressed ServedContent = %s, want %s", decompressed, upd.Baseline.Content)
	}
	got := result.Reports[0]
	wantDigest := "sha-256:" + digestOf(got.Content)
	if got.Digest != wantDigest {
		t.Errorf("Digest = %q, want sha-256 of the canonical (decompressed) content %q", got.Digest, wantDigest)
	}

	var entry catalog.CatalogEntry
	if err := json.Unmarshal(upd.SignedEntry, &entry); err != nil {
		t.Fatalf("parsing entry: %v", err)
	}
	if !strings.HasSuffix(entry.Baseline.URL, ".json.gz") {
		t.Errorf("expected baseline URL to end in .json.gz, got %+v", entry.Baseline)
	}
	if entry.Baseline.Size != int64(len(upd.Baseline.ServedContent)) {
		t.Errorf("baseline.Size = %d, want the compressed served size %d", entry.Baseline.Size, len(upd.Baseline.ServedContent))
	}
}

func TestPublish_InvalidSubmissionIsNonFatal(t *testing.T) {
	priv, keyID := testSigningKey(t)
	result, err := Publish(context.Background(), Params{
		Catalogs: []Submission{
			{CatalogID: "", Catalog: validCatalogJSON("bad")},                 // missing catalogId
			{CatalogID: "CAT-OK", Catalog: validCatalogJSON("CAT-OK")},        // valid
			{CatalogID: "CAT-BAD-JSON", Catalog: json.RawMessage(`not json`)}, // invalid JSON
		},
		SigningKey: priv, KeyID: keyID, Domain: "example.test",
	})
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if len(result.Errors) != 2 {
		t.Fatalf("expected 2 non-fatal errors, got %d: %+v", len(result.Errors), result.Errors)
	}
	if len(result.Reports) != 1 || result.Reports[0].CatalogID != "CAT-OK" {
		t.Fatalf("expected only CAT-OK to succeed, got %+v", result.Reports)
	}
	// A partial failure must still produce a valid signed entry for the
	// catalog that succeeded.
	if len(result.Publish.Updates) != 1 || len(result.Publish.Updates[0].SignedEntry) == 0 {
		t.Fatal("expected a signed entry for CAT-OK despite the other submissions failing")
	}
}

func TestPublish_InvalidSigningKeyFails(t *testing.T) {
	if _, err := Publish(context.Background(), Params{KeyID: "k1", SigningKey: []byte("too-short")}); err == nil {
		t.Fatal("expected error for an invalid signing key")
	}
}

func TestPublish_MissingKeyIDFails(t *testing.T) {
	priv, _ := testSigningKey(t)
	if _, err := Publish(context.Background(), Params{SigningKey: priv}); err == nil {
		t.Fatal("expected error for a missing KeyID")
	}
}

func TestCatalogPartURL_PlaceholderWhenUnconfigured(t *testing.T) {
	p := Params{}
	if got := p.catalogPartURL("CAT-1.v1.json", "json"); got != "pending-artifact-store://catalog/CAT-1.v1.json" {
		t.Errorf("unexpected placeholder URL: %q", got)
	}
	p.PublicBaseURL = "https://cdn.example.com/"
	if got := p.catalogPartURL("CAT-1.v1.json", "json"); got != "https://cdn.example.com/catalogs/CAT-1.v1.json" {
		t.Errorf("unexpected configured baseline URL: %q", got)
	}
	if got := p.catalogPartURL("CAT-1.v2.changes.json", "changes.json"); got != "https://cdn.example.com/catalogs/changes/CAT-1.v2.changes.json" {
		t.Errorf("unexpected configured change-file URL: %q", got)
	}
}

func TestIndexURL(t *testing.T) {
	if got := IndexURL(""); got != "pending-artifact-store://catalog-index.json" {
		t.Errorf("expected placeholder index URL when unset, got %q", got)
	}
	if got := IndexURL("https://example.test"); got != "https://example.test/index/becknCatalogs.index.json" {
		t.Errorf("expected index URL under publicBaseURL, got %q", got)
	}
}

func TestPublish_Incremental_NoPriorState_IsBaseline(t *testing.T) {
	priv, keyID := testSigningKey(t)
	result, err := Publish(context.Background(), Params{
		Catalogs:   []Submission{{CatalogID: "CAT-1", Catalog: mustCatalogWithItems("CAT-1", "ITEM-1", "ITEM-2")}},
		SigningKey: priv, KeyID: keyID, Domain: "example.test",
	})
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if len(result.Reports) != 1 {
		t.Fatalf("expected 1 outcome, got %+v", result.Reports)
	}
	got := result.Reports[0]
	if got.Mode != "baseline" || got.Version != 1 || !got.Changed {
		t.Errorf("unexpected outcome: %+v", got)
	}
	var file catalog.CatalogFileDoc
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
	priv, keyID := testSigningKey(t)
	cat := mustCatalogWithItems("CAT-1", "ITEM-1", "ITEM-2")
	prior := store.CatalogState{
		Catalog:      cat,
		BaselineFile: &catalog.FileEntry{Version: 1, URL: "file://baseline.json", Digest: "sha-256:abc"},
		EntryVersion: 1,
		CatalogType:  "REGULAR",
		IsActive:     true,
	}

	result, err := Publish(context.Background(), Params{
		Catalogs:   []Submission{{CatalogID: "CAT-1", Catalog: cat}},
		PriorState: map[string]store.CatalogState{"CAT-1": prior},
		SigningKey: priv, KeyID: keyID, Domain: "example.test",
	})
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}
	got := result.Reports[0]
	if got.Mode != "unchanged" || got.Changed || got.Version != 1 || got.Content != nil {
		t.Errorf("expected a no-op outcome, got %+v", got)
	}
	if got.EntryVersion != prior.EntryVersion {
		t.Errorf("expected entryVersion to stay %d on a total no-op, got %d", prior.EntryVersion, got.EntryVersion)
	}

	var entry catalog.CatalogEntry
	if err := json.Unmarshal(result.Publish.Updates[0].SignedEntry, &entry); err != nil {
		t.Fatalf("parsing catalog entry: %v", err)
	}
	if len(entry.Changes) != 0 || entry.Baseline.URL != "file://baseline.json" {
		t.Errorf("unexpected index entry carried forward: %+v", entry)
	}
}

func TestPublish_Incremental_ProducesChangeFile(t *testing.T) {
	priv, keyID := testSigningKey(t)
	priorCatalog := mustCatalogWithItems("CAT-1", "ITEM-1", "ITEM-2")
	// ITEM-1 updated (different descriptor.name), ITEM-2 removed, ITEM-3 added.
	nextCatalog := json.RawMessage(`{"id":"CAT-1","descriptor":{"name":"Test"},"provider":{},"resources":[` +
		`{"id":"ITEM-1","descriptor":{"name":"ITEM-1-updated"}},` +
		`{"id":"ITEM-3","descriptor":{"name":"ITEM-3"}}]}`)

	prior := store.CatalogState{
		Catalog:      priorCatalog,
		BaselineFile: &catalog.FileEntry{Version: 1, URL: "https://cdn.test/catalogs/CAT-1.v1.json", Digest: "sha-256:abc"},
	}

	result, err := Publish(context.Background(), Params{
		Catalogs:      []Submission{{CatalogID: "CAT-1", Catalog: nextCatalog}},
		PriorState:    map[string]store.CatalogState{"CAT-1": prior},
		PublicBaseURL: "https://cdn.test",
		SigningKey:    priv, KeyID: keyID, Domain: "example.test",
	})
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}
	got := result.Reports[0]
	if got.Mode != "change" || !got.Changed || got.Version != 2 {
		t.Fatalf("unexpected outcome: %+v", got)
	}

	var change catalog.ChangeFileDoc
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
		id, _ := catalog.ItemID(u)
		upsertIDs[id] = true
	}
	if !upsertIDs["ITEM-1"] || !upsertIDs["ITEM-3"] {
		t.Errorf("expected upserts for ITEM-1 (updated) and ITEM-3 (added), got %+v", upsertIDs)
	}

	var entry catalog.CatalogEntry
	if err := json.Unmarshal(result.Publish.Updates[0].SignedEntry, &entry); err != nil {
		t.Fatalf("parsing catalog entry: %v", err)
	}
	if entry.Baseline.URL != "https://cdn.test/catalogs/CAT-1.v1.json" {
		t.Errorf("expected baseline carried forward unchanged, got %+v", entry.Baseline)
	}
	if len(entry.Changes) != 1 || entry.Changes[0].FromVersion != 1 || entry.Changes[0].ToVersion != 2 || entry.Changes[0].URL != "https://cdn.test/catalogs/changes/CAT-1.v2.changes.json" {
		t.Errorf("unexpected change entry: %+v", entry.Changes)
	}
}

// TestPublish_MetadataOnlyChange_BumpsEntryVersionWithoutNewFile proves the
// NFH-014 §Versioning fix: editing only NetworkIds (no resource/offer/
// catalog-attribute change) must still bump EntryVersion and re-sign the
// entry, without producing a new baseline/change file or touching the
// file-lineage Version.
func TestPublish_MetadataOnlyChange_BumpsEntryVersionWithoutNewFile(t *testing.T) {
	priv, keyID := testSigningKey(t)
	cat := mustCatalogWithItems("CAT-1", "ITEM-1")
	prior := store.CatalogState{
		Catalog:      cat,
		BaselineFile: &catalog.FileEntry{Version: 1, URL: "file://baseline.json", Digest: "sha-256:abc"},
		EntryVersion: 3,
		CatalogType:  "REGULAR",
		IsActive:     true,
		NetworkIds:   []string{"old.network"},
	}

	result, err := Publish(context.Background(), Params{
		Catalogs:   []Submission{{CatalogID: "CAT-1", Catalog: cat, NetworkIds: []string{"new.network"}}},
		PriorState: map[string]store.CatalogState{"CAT-1": prior},
		SigningKey: priv, KeyID: keyID, Domain: "example.test",
	})
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}
	got := result.Reports[0]
	if got.Mode != "metadata" || !got.Changed || got.Content != nil || got.Version != 1 {
		t.Fatalf("expected a metadata-only outcome, got %+v", got)
	}
	if got.EntryVersion != 4 {
		t.Errorf("EntryVersion = %d, want 4 (bumped from prior 3)", got.EntryVersion)
	}

	var entry catalog.CatalogEntry
	if err := json.Unmarshal(result.Publish.Updates[0].SignedEntry, &entry); err != nil {
		t.Fatalf("parsing entry: %v", err)
	}
	if len(entry.NetworkIDs) != 1 || entry.NetworkIDs[0] != "new.network" {
		t.Errorf("expected the new NetworkIds in the re-signed entry, got %+v", entry.NetworkIDs)
	}
	if entry.Baseline.Version != 1 {
		t.Errorf("expected the baseline file reference to stay unchanged, got %+v", entry.Baseline)
	}
}

// TestPublish_ForceBaseline_KeepsPriorChangesListed proves the compaction
// grace-period fix (NFH-014 CON-TBD-32): a forced re-baseline must not
// reset Changes to nil -- the pre-compaction change files stay listed
// (not just hosted) so a DS mid-lineage can still reach equivalent content
// by applying them.
func TestPublish_ForceBaseline_KeepsPriorChangesListed(t *testing.T) {
	priv, keyID := testSigningKey(t)
	cat := mustCatalogWithItems("CAT-1", "ITEM-1")
	priorChange := catalog.FileEntry{FromVersion: 1, ToVersion: 2, URL: "file://v2.changes.json", Digest: "sha-256:def"}
	prior := store.CatalogState{
		Catalog:      cat,
		BaselineFile: &catalog.FileEntry{Version: 1, URL: "file://v1.json", Digest: "sha-256:abc"},
		ChangeFiles:  []catalog.FileEntry{priorChange},
		EntryVersion: 2,
		CatalogType:  "REGULAR",
	}

	result, err := Publish(context.Background(), Params{
		Catalogs:      []Submission{{CatalogID: "CAT-1", Catalog: cat}},
		PriorState:    map[string]store.CatalogState{"CAT-1": prior},
		ForceBaseline: true,
		SigningKey:    priv, KeyID: keyID, Domain: "example.test",
	})
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}
	got := result.Reports[0]
	if got.Mode != "baseline" || got.Version != 3 {
		t.Fatalf("unexpected outcome: %+v", got)
	}

	var entry catalog.CatalogEntry
	if err := json.Unmarshal(result.Publish.Updates[0].SignedEntry, &entry); err != nil {
		t.Fatalf("parsing entry: %v", err)
	}
	if entry.Baseline.Version != 3 {
		t.Fatalf("expected the new baseline at version 3, got %+v", entry.Baseline)
	}
	if len(entry.Changes) != 1 || entry.Changes[0].FromVersion != 1 || entry.Changes[0].ToVersion != 2 || entry.Changes[0].URL != priorChange.URL {
		t.Errorf("expected the pre-compaction change file to stay listed, got %+v", entry.Changes)
	}
}

// TestPublish_CompactionChangeCountThreshold_TriggersBaseline proves
// NFH-014 §10.1's automatic compaction trigger: once a catalog already
// has at least CompactionChangeCountThreshold pending change files, the
// next content-changing publish emits a fresh baseline (keeping the
// pre-compaction changes listed, same as ForceBaseline) instead of one
// more change file.
func TestPublish_CompactionChangeCountThreshold_TriggersBaseline(t *testing.T) {
	priv, keyID := testSigningKey(t)
	priorCatalog := mustCatalogWithItems("CAT-1", "ITEM-1")
	nextCatalog := mustCatalogWithItems("CAT-1", "ITEM-1", "ITEM-2")
	prior := store.CatalogState{
		Catalog:      priorCatalog,
		BaselineFile: &catalog.FileEntry{Version: 1, URL: "file://v1.json", Digest: "sha-256:abc"},
		ChangeFiles: []catalog.FileEntry{
			{FromVersion: 1, ToVersion: 2, URL: "file://v2.changes.json", Digest: "sha-256:def"},
			{FromVersion: 2, ToVersion: 3, URL: "file://v3.changes.json", Digest: "sha-256:ghi"},
		},
		EntryVersion: 3,
		CatalogType:  "REGULAR",
	}

	result, err := Publish(context.Background(), Params{
		Catalogs:                       []Submission{{CatalogID: "CAT-1", Catalog: nextCatalog}},
		PriorState:                     map[string]store.CatalogState{"CAT-1": prior},
		CompactionChangeCountThreshold: 2,
		SigningKey:                     priv, KeyID: keyID, Domain: "example.test",
	})
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}
	got := result.Reports[0]
	if got.Mode != "baseline" || got.Version != 4 {
		t.Fatalf("expected auto-compaction to emit a fresh baseline at version 4, got %+v", got)
	}

	var entry catalog.CatalogEntry
	if err := json.Unmarshal(result.Publish.Updates[0].SignedEntry, &entry); err != nil {
		t.Fatalf("parsing entry: %v", err)
	}
	if len(entry.Changes) != 2 {
		t.Errorf("expected the 2 pre-compaction change files to stay listed, got %+v", entry.Changes)
	}
}

// TestPublish_CompactionChangeCountThreshold_NotYetReached proves the
// trigger doesn't fire early: one pending change file against a
// threshold of 2 must still produce an ordinary change file.
func TestPublish_CompactionChangeCountThreshold_NotYetReached(t *testing.T) {
	priv, keyID := testSigningKey(t)
	priorCatalog := mustCatalogWithItems("CAT-1", "ITEM-1")
	nextCatalog := mustCatalogWithItems("CAT-1", "ITEM-1", "ITEM-2")
	prior := store.CatalogState{
		Catalog:      priorCatalog,
		BaselineFile: &catalog.FileEntry{Version: 1, URL: "file://v1.json", Digest: "sha-256:abc"},
		ChangeFiles:  []catalog.FileEntry{{FromVersion: 1, ToVersion: 2, URL: "file://v2.changes.json", Digest: "sha-256:def"}},
		EntryVersion: 2,
		CatalogType:  "REGULAR",
	}

	result, err := Publish(context.Background(), Params{
		Catalogs:                       []Submission{{CatalogID: "CAT-1", Catalog: nextCatalog}},
		PriorState:                     map[string]store.CatalogState{"CAT-1": prior},
		CompactionChangeCountThreshold: 2,
		SigningKey:                     priv, KeyID: keyID, Domain: "example.test",
	})
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if got := result.Reports[0]; got.Mode != "change" {
		t.Errorf("expected an ordinary change file below threshold, got %+v", got)
	}
}

// TestPublish_CompactionSizeRatioThreshold_TriggersBaseline covers the
// size-based trigger: combined pending-change size / baseline size at or
// above the configured ratio compacts instead of adding another change.
func TestPublish_CompactionSizeRatioThreshold_TriggersBaseline(t *testing.T) {
	priv, keyID := testSigningKey(t)
	priorCatalog := mustCatalogWithItems("CAT-1", "ITEM-1")
	nextCatalog := mustCatalogWithItems("CAT-1", "ITEM-1", "ITEM-2")
	prior := store.CatalogState{
		Catalog:      priorCatalog,
		BaselineFile: &catalog.FileEntry{Version: 1, URL: "file://v1.json", Digest: "sha-256:abc", Size: 100},
		ChangeFiles:  []catalog.FileEntry{{FromVersion: 1, ToVersion: 2, URL: "file://v2.changes.json", Digest: "sha-256:def", Size: 60}},
		EntryVersion: 2,
		CatalogType:  "REGULAR",
	}

	result, err := Publish(context.Background(), Params{
		Catalogs:                     []Submission{{CatalogID: "CAT-1", Catalog: nextCatalog}},
		PriorState:                   map[string]store.CatalogState{"CAT-1": prior},
		CompactionSizeRatioThreshold: 0.5,
		SigningKey:                   priv, KeyID: keyID, Domain: "example.test",
	})
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if got := result.Reports[0]; got.Mode != "baseline" {
		t.Errorf("expected the size ratio (60/100 = 0.6 >= 0.5) to trigger compaction, got %+v", got)
	}
}

// TestPublish_CompactionThreshold_NeverForcesBaselineOnNoOp proves the
// trigger is gated on an actual content change: a catalog already over
// threshold, resubmitted with unchanged content, must stay a no-op --
// never a spurious baseline republish.
func TestPublish_CompactionThreshold_NeverForcesBaselineOnNoOp(t *testing.T) {
	priv, keyID := testSigningKey(t)
	cat := mustCatalogWithItems("CAT-1", "ITEM-1")
	prior := store.CatalogState{
		Catalog:      cat,
		BaselineFile: &catalog.FileEntry{Version: 1, URL: "file://v1.json", Digest: "sha-256:abc"},
		ChangeFiles:  []catalog.FileEntry{{FromVersion: 1, ToVersion: 2, URL: "file://v2.changes.json", Digest: "sha-256:def"}},
		EntryVersion: 2,
		CatalogType:  "REGULAR",
		IsActive:     true,
	}

	result, err := Publish(context.Background(), Params{
		Catalogs:                       []Submission{{CatalogID: "CAT-1", Catalog: cat}},
		PriorState:                     map[string]store.CatalogState{"CAT-1": prior},
		CompactionChangeCountThreshold: 1,
		SigningKey:                     priv, KeyID: keyID, Domain: "example.test",
	})
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if got := result.Reports[0]; got.Mode != "unchanged" || got.Changed {
		t.Errorf("expected a no-op despite being over the compaction threshold, got %+v", got)
	}
}

func TestPublish_Retire_ProducesTombstone(t *testing.T) {
	priv, keyID := testSigningKey(t)
	result, err := Publish(context.Background(), Params{
		Retire:     []string{"CAT-OLD"},
		SigningKey: priv, KeyID: keyID, Domain: "example.test",
	})
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}

	if len(result.Publish.Retirements) != 1 {
		t.Fatalf("expected 1 tombstone entry, got %d", len(result.Publish.Retirements))
	}
	var entry catalog.CatalogEntry
	if err := json.Unmarshal(result.Publish.Retirements[0].SignedEntry, &entry); err != nil {
		t.Fatalf("parsing entry: %v", err)
	}
	if entry.CatalogID != "CAT-OLD" || !entry.IsRetired() || entry.EntryVersion != 1 {
		t.Errorf("unexpected tombstone: %+v", entry)
	}
	if entry.Baseline.URL != "" || len(entry.Changes) != 0 || entry.IsActive != nil {
		t.Errorf("expected no files/isActive on a tombstone, got %+v", entry)
	}
}

// TestPublish_RetireWithPriorState_KeepsMetadataAndBumpsEntryVersion
// proves NFH-014 Appendix A Example 4's retired entry shape: catalogType/
// networkIds/schemaTypes survive retirement (only isActive/baseline/
// changes are dropped), and EntryVersion continues its lineage from the
// catalog's prior EntryVersion rather than resetting to 1.
func TestPublish_RetireWithPriorState_KeepsMetadataAndBumpsEntryVersion(t *testing.T) {
	priv, keyID := testSigningKey(t)
	result, err := Publish(context.Background(), Params{
		Retire: []string{"CAT-OLD"},
		PriorState: map[string]store.CatalogState{
			"CAT-OLD": {
				EntryVersion: 5,
				CatalogType:  "REGULAR",
				NetworkIds:   []string{"ion.example"},
				SchemaTypes:  []string{"https://schema.example/1.0.0/context.jsonld"},
			},
		},
		SigningKey: priv, KeyID: keyID, Domain: "example.test",
	})
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}

	if len(result.Publish.Retirements) != 1 {
		t.Fatalf("expected 1 tombstone entry, got %d", len(result.Publish.Retirements))
	}
	var entry catalog.CatalogEntry
	if err := json.Unmarshal(result.Publish.Retirements[0].SignedEntry, &entry); err != nil {
		t.Fatalf("parsing entry: %v", err)
	}
	if entry.EntryVersion != 6 {
		t.Errorf("EntryVersion = %d, want 6 (bumped from prior 5)", entry.EntryVersion)
	}
	if entry.CatalogType != "REGULAR" || len(entry.NetworkIDs) != 1 || len(entry.SchemaTypes) != 1 {
		t.Errorf("expected prior metadata to survive retirement, got %+v", entry)
	}
	if entry.IsActive != nil || entry.Baseline.URL != "" || len(entry.Changes) != 0 {
		t.Errorf("expected isActive/baseline/changes dropped on retirement, got %+v", entry)
	}
	if result.Publish.Retirements[0].Latest != nil {
		t.Errorf("expected no final latest write when the catalog never had \"latest\" published, got %+v", result.Publish.Retirements[0])
	}
}

// TestPublish_RetireWithLatestPublished_WritesFinalTombstone proves
// CON-TBD-38: retiring a catalog whose PriorState.LatestPublished is set
// must produce a final, self-signed CatalogFileDoc carrying retiredAt --
// regardless of whether PublishLatest is on for this call, since this is
// cleaning up a file that already exists, not deciding whether to start
// publishing a new one.
func TestPublish_RetireWithLatestPublished_WritesFinalTombstone(t *testing.T) {
	priv, keyID := testSigningKey(t)
	cat := mustCatalogWithItems("CAT-OLD", "ITEM-1")
	result, err := Publish(context.Background(), Params{ // PublishLatest deliberately off
		Retire: []string{"CAT-OLD"},
		PriorState: map[string]store.CatalogState{
			"CAT-OLD": {
				Catalog:         cat,
				BaselineFile:    &catalog.FileEntry{Version: 3, URL: "file://v3.json", Digest: "sha-256:abc"},
				EntryVersion:    5,
				CatalogType:     "REGULAR",
				LatestPublished: true,
			},
		},
		SigningKey: priv, KeyID: keyID, Domain: "example.test",
	})
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if len(result.Publish.Retirements) != 1 || result.Publish.Retirements[0].Latest == nil {
		t.Fatalf("expected 1 retirement with a final latest write, got %+v", result.Publish.Retirements)
	}
	rl := result.Publish.Retirements[0]
	if rl.CatalogID != "CAT-OLD" || rl.Latest.Content == nil {
		t.Fatalf("unexpected retirement entry: %+v", rl)
	}
	var file catalog.CatalogFileDoc
	if err := json.Unmarshal(rl.Latest.Content, &file); err != nil {
		t.Fatalf("parsing final tombstone content: %v", err)
	}
	if file.CatalogID != "CAT-OLD" || file.Version != 3 || file.RetiredAt == nil {
		t.Errorf("expected the final tombstone to carry catalogId/version/retiredAt, got %+v", file)
	}
	if file.Signature.Value == "" {
		t.Error("expected the final tombstone to be self-signed")
	}
}
