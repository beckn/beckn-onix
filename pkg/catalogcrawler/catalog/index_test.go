package catalog

import (
	"encoding/json"
	"testing"
)

// exampleIndex mirrors the File Specifications example (a public catalog with
// a baseline + one change file, a network-scoped catalog with a signed-request
// download gate, and a retired tombstone) using neutral placeholder names.
const exampleIndex = `{
  "participantId": "publisher.example.com",
  "version": 2,
  "next_update": "2026-02-06T09:00:00Z",
  "catalogs": [
    {
      "catalogId": "publisher.example.com/electronics-2026",
      "status": "ACTIVE",
      "schemaTypes": ["https://schema.beckn.org/retail/schema/1.1.0/context.jsonld"],
      "baseline": {
        "version": 1,
        "url": "https://cdn.publisher.example.com/beckn/electronics-2026.v1.json",
        "size": 1848320,
        "digest": "sha-256:9f2c",
        "signature": { "keyId": "key-1", "value": "abc", "validUntil": "2026-07-30T09:00:00Z" }
      },
      "changes": [
        {
          "version": 2,
          "url": "https://cdn.publisher.example.com/beckn/electronics-2026.v2.changes.json",
          "size": 18240,
          "digest": "sha-256:5b1a",
          "signature": { "keyId": "key-1", "value": "def", "validUntil": "2026-07-30T09:00:00Z" }
        }
      ]
    },
    {
      "catalogId": "publisher.example.com/partner-catalog-2026",
      "status": "ACTIVE",
      "networkIds": ["network-a.example.com"],
      "authMethods": [
        { "method": "signed-request", "header": "Authorization", "signedHeaders": ["(created)","(expires)","(request-target)","host","digest"], "freshnessSeconds": 60 }
      ],
      "baseline": { "version": 12, "url": "https://cdn.publisher.example.com/beckn/partner-catalog-2026.v12.json", "size": 202400, "digest": "sha-256:1c9a", "signature": { "keyId": "key-1" } },
      "changes": []
    },
    {
      "catalogId": "publisher.example.com/electronics-2025",
      "status": "RETIRED",
      "retiredAt": "2026-01-31T00:00:00Z"
    }
  ]
}`

func TestModel_ParsesSpecExampleIndex(t *testing.T) {
	var idx Index
	if err := json.Unmarshal([]byte(exampleIndex), &idx); err != nil {
		t.Fatalf("unmarshal index: %v", err)
	}
	if idx.ParticipantID != "publisher.example.com" || idx.Version != 2 {
		t.Fatalf("index header = %+v", idx)
	}
	if len(idx.Catalogs) != 3 {
		t.Fatalf("catalogs = %d, want 3", len(idx.Catalogs))
	}

	// 1) Public ACTIVE catalog: baseline v1 + change v2 -> latest 2, public.
	pub := idx.Catalogs[0]
	if !pub.IsPublic() {
		t.Errorf("catalog 0 should be public (no networkIds)")
	}
	if pub.LatestVersion() != 2 {
		t.Errorf("catalog 0 LatestVersion = %d, want 2", pub.LatestVersion())
	}
	if pub.Baseline.Signature.KeyID != "key-1" {
		t.Errorf("baseline signature keyId = %q, want key-1", pub.Baseline.Signature.KeyID)
	}
	if len(pub.Changes) != 1 || pub.Changes[0].Digest != "sha-256:5b1a" {
		t.Errorf("change entry = %+v", pub.Changes)
	}

	// 2) Network-scoped catalog with a signed-request gate.
	restricted := idx.Catalogs[1]
	if restricted.IsPublic() {
		t.Errorf("catalog 1 should be network-scoped")
	}
	if len(restricted.AuthMethods) != 1 || restricted.AuthMethods[0].Method != "signed-request" || restricted.AuthMethods[0].FreshnessSeconds != 60 {
		t.Errorf("authMethods = %+v", restricted.AuthMethods)
	}
	if take, visible := Select(restricted, []string{"network-a.example.com"}); !take || len(visible) != 1 {
		t.Errorf("network catalog should be taken by a member crawler; take=%v visible=%v", take, visible)
	}

	// 3) RETIRED tombstone (no baseline/changes) still decides to retire.
	tomb := idx.Catalogs[2]
	if tomb.Status != StatusRetired || tomb.RetiredAt == "" {
		t.Errorf("tombstone = %+v", tomb)
	}
	if d := Decide(tomb, 12, true); d.Action != ActionRetire {
		t.Errorf("Decide(retired) = %v, want retire", d.Action)
	}
}
