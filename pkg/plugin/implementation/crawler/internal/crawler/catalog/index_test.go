package catalog

// index_test.go — tests that the index model parses RFC NFH-014's Appendix A
// example index and that LatestVersion / IsPublic / IsRetired / IsPaused /
// ResolveScope / DetectChange behave correctly on it.

import (
	"encoding/json"
	"testing"
)

// exampleIndex mirrors NFH-014 Appendix A's example 4 (one ACTIVE catalog
// with a baseline + one change file and a MASTER dependency, one PAUSED
// seasonal catalog, and one RETIRED tombstone), using neutral placeholder
// names.
const exampleIndex = `{
  "nodeId": "publisher.example.com",
  "next_update": "2026-02-06T09:00:00Z",
  "catalogs": [
    {
      "catalogId": "publisher.example.com/electronics-2026",
      "entryVersion": 7,
      "catalogType": "REGULAR",
      "dependencies": { "masters": [
        { "catalogId": "publisher.example.com/electronics-master", "indexUrl": "https://cdn.publisher.example.com/beckn/catalog-index.json" }
      ] },
      "isActive": true,
      "schemaTypes": ["https://schema.beckn.org/retail/schema/1.1.0/context.jsonld"],
      "baseline": {
        "version": 1,
        "url": "https://cdn.publisher.example.com/beckn/electronics-2026.v1.json",
        "size": 1848320,
        "digest": "sha-256:9f2c"
      },
      "changes": [
        {
          "version": 2,
          "url": "https://cdn.publisher.example.com/beckn/electronics-2026.v2.changes.json",
          "size": 18240,
          "digest": "sha-256:5b1a"
        }
      ],
      "signature": { "keyId": "key-1", "value": "abc" }
    },
    {
      "catalogId": "publisher.example.com/partner-catalog-2026",
      "entryVersion": 13,
      "catalogType": "REGULAR",
      "isActive": false,
      "networkIds": ["network-a.example.com"],
      "baseline": { "version": 12, "url": "https://cdn.publisher.example.com/beckn/partner-catalog-2026.v12.json", "size": 202400, "digest": "sha-256:1c9a" },
      "changes": [],
      "signature": { "keyId": "key-1", "value": "ghi" }
    },
    {
      "catalogId": "publisher.example.com/electronics-2025",
      "entryVersion": 21,
      "retiredAt": "2026-01-31T00:00:00Z",
      "signature": { "keyId": "key-1", "value": "jkl" }
    }
  ]
}`

func TestModel_ParsesRFCExampleIndex(t *testing.T) {
	var idx Index
	if err := json.Unmarshal([]byte(exampleIndex), &idx); err != nil {
		t.Fatalf("unmarshal index: %v", err)
	}
	if idx.NodeID != "publisher.example.com" {
		t.Fatalf("index header = %+v", idx)
	}
	if len(idx.Catalogs) != 3 {
		t.Fatalf("catalogs = %d, want 3", len(idx.Catalogs))
	}

	// 1) Public ACTIVE catalog: baseline v1 + change v2 -> latest 2, public,
	// with a MASTER dependency.
	pub := idx.Catalogs[0]
	if !pub.IsPublic() {
		t.Errorf("catalog 0 should be public (no networkIds)")
	}
	if pub.LatestVersion() != 2 {
		t.Errorf("catalog 0 LatestVersion = %d, want 2", pub.LatestVersion())
	}
	if pub.EntryVersion != 7 {
		t.Errorf("entryVersion = %d, want 7", pub.EntryVersion)
	}
	if pub.IsActive == nil || !*pub.IsActive {
		t.Errorf("catalog 0 should be active, got %+v", pub.IsActive)
	}
	if pub.IsRetired() || pub.IsPaused() {
		t.Errorf("catalog 0 should be neither retired nor paused")
	}
	if pub.Dependencies == nil || len(pub.Dependencies.Masters) != 1 || pub.Dependencies.Masters[0].CatalogID != "publisher.example.com/electronics-master" {
		t.Errorf("dependencies = %+v", pub.Dependencies)
	}
	if pub.Signature.KeyID != "key-1" {
		t.Errorf("entry signature keyId = %q, want key-1", pub.Signature.KeyID)
	}
	if len(pub.Changes) != 1 || pub.Changes[0].Digest != "sha-256:5b1a" {
		t.Errorf("change entry = %+v", pub.Changes)
	}

	// 2) Network-scoped, explicitly PAUSED catalog: still indexed/taken, just
	// not currently active.
	paused := idx.Catalogs[1]
	if paused.IsPublic() {
		t.Errorf("catalog 1 should be network-scoped")
	}
	if !paused.IsPaused() {
		t.Errorf("catalog 1 should be paused, got isActive=%+v", paused.IsActive)
	}
	if paused.IsRetired() {
		t.Errorf("catalog 1 should not be retired")
	}
	if take, visible := ResolveScope(paused, []string{"network-a.example.com"}); !take || len(visible) != 1 {
		t.Errorf("network catalog should be taken by a member crawler; take=%v visible=%v", take, visible)
	}

	// 3) RETIRED tombstone (no baseline/changes/isActive) still decides to retire.
	tomb := idx.Catalogs[2]
	if !tomb.IsRetired() || tomb.RetiredAt == "" {
		t.Errorf("tombstone = %+v", tomb)
	}
	if tomb.IsActive != nil {
		t.Errorf("a retired entry must not carry isActive, got %+v", tomb.IsActive)
	}
	if d := DetectChange(tomb, 12, 0, true); d.Action != ActionRetire {
		t.Errorf("DetectChange(retired) = %v, want retire", d.Action)
	}
}
