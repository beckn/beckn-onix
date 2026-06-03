package model

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// validNodeManifest returns a fully populated valid NodeManifest for use in tests.
func validNodeManifest(subscriberID string, now time.Time) NodeManifest {
	return NodeManifest{
		ManifestVersion: "1.0",
		ManifestType:    NodeManifestType,
		SubscriberID:    subscriberID,
		Schema: NodeManifestSchema{
			SchemaObjects: []SchemaObject{
				{
					ContextURL: "https://schema.beckn.io/Order/2.0/context.jsonld",
					Type:       "beckn:Order",
				},
				{
					ContextURL: "https://schema.beckn.io/Fulfillment/2.0/context.jsonld",
					Type:       "beckn:Fulfillment",
				},
			},
		},
		Governance: NodeManifestGovernance{
			EffectiveFrom:  now.Add(-1 * time.Hour).Format(time.RFC3339),
			EffectiveUntil: now.Add(1 * time.Hour).Format(time.RFC3339),
		},
	}
}

func TestParseNodeManifest(t *testing.T) {
	rawYAML := `
manifestVersion: "1.0"
manifestType: "node-manifest"
subscriberId: "nfh.global/subscribers.beckn.one/bpp.energy-provider.com"
schema:
  schemaObjects:
    - contextUrl: "https://schema.beckn.io/Order/2.0/context.jsonld"
      type: "beckn:Order"
    - contextUrl: "https://schema.beckn.io/Order/1.0/context.jsonld"
      type: "beckn:Order"
governance:
  effectiveFrom: "2026-01-01T00:00:00Z"
  effectiveUntil: "2027-01-01T00:00:00Z"
`
	m, err := ParseNodeManifest([]byte(rawYAML))
	if err != nil {
		t.Fatalf("ParseNodeManifest returned unexpected error: %v", err)
	}
	if m.ManifestVersion != "1.0" {
		t.Errorf("manifestVersion: got %q, want %q", m.ManifestVersion, "1.0")
	}
	if m.ManifestType != NodeManifestType {
		t.Errorf("manifestType: got %q, want %q", m.ManifestType, NodeManifestType)
	}
	if m.SubscriberID != "nfh.global/subscribers.beckn.one/bpp.energy-provider.com" {
		t.Errorf("subscriberId: got %q", m.SubscriberID)
	}
	if len(m.Schema.SchemaObjects) != 2 {
		t.Fatalf("schemaObjects: got %d, want 2", len(m.Schema.SchemaObjects))
	}
	if m.Schema.SchemaObjects[0].ContextURL != "https://schema.beckn.io/Order/2.0/context.jsonld" {
		t.Errorf("schemaObjects[0].contextUrl: got %q", m.Schema.SchemaObjects[0].ContextURL)
	}
	if m.Schema.SchemaObjects[0].Type != "beckn:Order" {
		t.Errorf("schemaObjects[0].type: got %q", m.Schema.SchemaObjects[0].Type)
	}
	if m.Governance.EffectiveFrom != "2026-01-01T00:00:00Z" {
		t.Errorf("governance.effectiveFrom: got %q", m.Governance.EffectiveFrom)
	}
	if m.Governance.EffectiveUntil != "2027-01-01T00:00:00Z" {
		t.Errorf("governance.effectiveUntil: got %q", m.Governance.EffectiveUntil)
	}
}

func TestParseNodeManifest_InvalidYAML(t *testing.T) {
	_, err := ParseNodeManifest([]byte(":\tinvalid yaml{{{"))
	if err == nil {
		t.Fatal("expected error for invalid YAML, got nil")
	}
}

func TestValidateNodeManifest(t *testing.T) {
	now := time.Now().UTC()
	const validID = "nfh.global/subscribers.beckn.one/bpp.energy-provider.com"

	tests := []struct {
		name       string
		manifest   func() NodeManifest
		wantErrSub string
	}{
		{
			name:     "valid manifest",
			manifest: func() NodeManifest { return validNodeManifest(validID, now) },
		},
		{
			name: "valid manifest without effectiveUntil",
			manifest: func() NodeManifest {
				m := validNodeManifest(validID, now)
				m.Governance.EffectiveUntil = ""
				return m
			},
		},
		{
			name: "missing manifestVersion",
			manifest: func() NodeManifest {
				m := validNodeManifest(validID, now)
				m.ManifestVersion = ""
				return m
			},
			wantErrSub: "missing manifestVersion",
		},
		{
			name: "wrong manifestType",
			manifest: func() NodeManifest {
				m := validNodeManifest(validID, now)
				m.ManifestType = "network-manifest"
				return m
			},
			wantErrSub: `must have manifestType="node-manifest"`,
		},
		{
			name: "blank subscriberId",
			manifest: func() NodeManifest {
				m := validNodeManifest(validID, now)
				m.SubscriberID = ""
				return m
			},
			wantErrSub: "missing subscriberId",
		},
		{
			name: "subscriberId with only one part",
			manifest: func() NodeManifest {
				m := validNodeManifest(validID, now)
				m.SubscriberID = "bpp.energy-provider.com"
				return m
			},
			wantErrSub: "namespace/registry/recordId format",
		},
		{
			name: "subscriberId with two parts",
			manifest: func() NodeManifest {
				m := validNodeManifest(validID, now)
				m.SubscriberID = "nfh.global/subscribers.beckn.one"
				return m
			},
			wantErrSub: "namespace/registry/recordId format",
		},
		{
			name: "subscriberId with empty segment",
			manifest: func() NodeManifest {
				m := validNodeManifest(validID, now)
				m.SubscriberID = "nfh.global//bpp.energy-provider.com"
				return m
			},
			wantErrSub: "namespace/registry/recordId format",
		},
		{
			name: "subscriberId with four parts",
			manifest: func() NodeManifest {
				m := validNodeManifest(validID, now)
				m.SubscriberID = "nfh.global/subscribers.beckn.one/bpp.energy-provider.com/extra"
				return m
			},
			wantErrSub: "namespace/registry/recordId format",
		},
		{
			name: "subscriberId mismatch",
			manifest: func() NodeManifest {
				m := validNodeManifest(validID, now)
				m.SubscriberID = "nfh.global/subscribers.beckn.one/other.com"
				return m
			},
			wantErrSub: "does not match expected",
		},
		{
			name: "empty schemaObjects",
			manifest: func() NodeManifest {
				m := validNodeManifest(validID, now)
				m.Schema.SchemaObjects = nil
				return m
			},
			wantErrSub: "at least one schema object",
		},
		{
			name: "schema object missing contextUrl",
			manifest: func() NodeManifest {
				m := validNodeManifest(validID, now)
				m.Schema.SchemaObjects[0].ContextURL = ""
				return m
			},
			wantErrSub: "missing contextUrl",
		},
		{
			name: "schema object missing type",
			manifest: func() NodeManifest {
				m := validNodeManifest(validID, now)
				m.Schema.SchemaObjects[0].Type = ""
				return m
			},
			wantErrSub: "missing type",
		},
		{
			name: "invalid effectiveFrom",
			manifest: func() NodeManifest {
				m := validNodeManifest(validID, now)
				m.Governance.EffectiveFrom = "not-a-date"
				return m
			},
			wantErrSub: "invalid governance.effectiveFrom",
		},
		{
			name: "effectiveFrom in the future",
			manifest: func() NodeManifest {
				m := validNodeManifest(validID, now)
				m.Governance.EffectiveFrom = now.Add(2 * time.Hour).Format(time.RFC3339)
				m.Governance.EffectiveUntil = now.Add(4 * time.Hour).Format(time.RFC3339)
				return m
			},
			wantErrSub: "not active until",
		},
		{
			name: "invalid effectiveUntil",
			manifest: func() NodeManifest {
				m := validNodeManifest(validID, now)
				m.Governance.EffectiveUntil = "not-a-date"
				return m
			},
			wantErrSub: "invalid governance.effectiveUntil",
		},
		{
			name: "effectiveUntil before effectiveFrom",
			manifest: func() NodeManifest {
				m := validNodeManifest(validID, now)
				m.Governance.EffectiveUntil = now.Add(-2 * time.Hour).Format(time.RFC3339)
				return m
			},
			wantErrSub: "effectiveUntil later than governance.effectiveFrom",
		},
		{
			name: "expired manifest",
			manifest: func() NodeManifest {
				m := validNodeManifest(validID, now)
				m.Governance.EffectiveFrom = now.Add(-3 * time.Hour).Format(time.RFC3339)
				m.Governance.EffectiveUntil = now.Add(-1 * time.Hour).Format(time.RFC3339)
				return m
			},
			wantErrSub: "expired at",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := tt.manifest()
			err := m.Validate(validID, now)

			if tt.wantErrSub == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tt.wantErrSub)
			}
			if !strings.Contains(err.Error(), tt.wantErrSub) {
				t.Fatalf("expected error containing %q, got: %v", tt.wantErrSub, err)
			}
		})
	}
}

func TestValidateNodeManifest_Nil(t *testing.T) {
	var m *NodeManifest
	err := m.Validate("nfh.global/subscribers.beckn.one/bpp.energy-provider.com", time.Now())
	if err == nil {
		t.Fatal("expected error for nil manifest, got nil")
	}
	if !strings.Contains(err.Error(), "cannot be nil") {
		t.Fatalf("expected 'cannot be nil' error, got: %v", err)
	}
}

func TestManifestDocument_SubscriberID(t *testing.T) {
	doc := ManifestDocument{
		NetworkID:    "",
		SubscriberID: "nfh.global/subscribers.beckn.one/bpp.energy-provider.com",
		Digest:       "abc123",
		SourceURL:    "https://example.com/manifest.yaml",
		Verified:     true,
	}

	// Serialise
	b, err := json.Marshal(doc)
	if err != nil {
		t.Fatalf("json.Marshal failed: %v", err)
	}

	// SubscriberID must be present in JSON output
	if !strings.Contains(string(b), `"subscriber_id"`) {
		t.Errorf("expected subscriber_id in JSON, got: %s", string(b))
	}

	// Deserialise
	var got ManifestDocument
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("json.Unmarshal failed: %v", err)
	}
	if got.SubscriberID != doc.SubscriberID {
		t.Errorf("SubscriberID round-trip: got %q, want %q", got.SubscriberID, doc.SubscriberID)
	}

	// Existing cache entries without subscriber_id must deserialise without error
	legacy := `{"network_id":"nfh.global/testnet","content":"","digest":"x","source_url":"https://x","signature_url":"","verified":true,"fetched_at":"2026-01-01T00:00:00Z"}`
	var legacyDoc ManifestDocument
	if err := json.Unmarshal([]byte(legacy), &legacyDoc); err != nil {
		t.Fatalf("legacy document unmarshal failed: %v", err)
	}
	if legacyDoc.SubscriberID != "" {
		t.Errorf("legacy document should have empty SubscriberID, got %q", legacyDoc.SubscriberID)
	}
}
