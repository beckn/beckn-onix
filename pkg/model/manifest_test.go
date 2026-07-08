package model

import (
	"strings"
	"testing"
	"time"
)

func validNetworkManifestForTest(networkID string, now time.Time) NetworkManifest {
	signed := true
	return NetworkManifest{
		ManifestVersion: "1.0",
		ManifestType:    NetworkManifestType,
		NetworkID:       networkID,
		ReleaseID:       "2026.02",
		Publisher: NetworkManifestPublisher{
			Role:   "NFO",
			Domain: "nfh.global",
		},
		Policies: &NetworkManifestPolicies{
			Type:   PolicyTypeRego,
			Source: PolicySourceFile,
			File: &NetworkManifestFile{
				ID:                       "network-policy-file",
				URL:                      "https://example.com/policy.rego",
				PolicyQueryPath:          "data.logistics.result",
				Signed:                   true,
				SignatureURL:             "https://example.com/policy.rego.sig",
				SigningPublicKeyLookupURL: "https://example.com/public-key",
			},
		},
		Governance: NetworkManifestGovernance{
			EffectiveFrom:  now.Add(-1 * time.Hour).Format(time.RFC3339),
			EffectiveUntil: now.Add(1 * time.Hour).Format(time.RFC3339),
			Signed:         &signed,
		},
	}
}

func TestValidateNetworkManifest(t *testing.T) {
	now := time.Date(2026, time.April, 22, 12, 0, 0, 0, time.UTC)
	expectedNetworkID := "nfh.global/testnet"

	tests := []struct {
		name       string
		mutate     func(*NetworkManifest)
		wantErrSub string
	}{
		{
			name: "valid",
		},
		{
			name: "invalid manifest type",
			mutate: func(manifest *NetworkManifest) {
				manifest.ManifestType = "other"
			},
			wantErrSub: `must have manifestType="network-manifest"`,
		},
		{
			name: "missing policies section",
			mutate: func(manifest *NetworkManifest) {
				manifest.Policies = nil
			},
			wantErrSub: "missing policies section",
		},
		{
			name: "network mismatch",
			mutate: func(manifest *NetworkManifest) {
				manifest.NetworkID = "example/logistics"
			},
			wantErrSub: `does not match configured network "nfh.global/testnet"`,
		},
		{
			name: "invalid effective until",
			mutate: func(manifest *NetworkManifest) {
				manifest.Governance.EffectiveUntil = "not-a-timestamp"
			},
			wantErrSub: "invalid governance.effectiveUntil",
		},
		{
			name: "expired manifest",
			mutate: func(manifest *NetworkManifest) {
				manifest.Governance.EffectiveUntil = now.Add(-1 * time.Minute).Format(time.RFC3339)
			},
			wantErrSub: "expired at",
		},
		{
			name: "unsupported source",
			mutate: func(manifest *NetworkManifest) {
				manifest.Policies.Source = "archive"
				manifest.Policies.File = nil
			},
			wantErrSub: `uses unsupported policies.source "archive"`,
		},
		{
			name: "signed file missing signature url",
			mutate: func(manifest *NetworkManifest) {
				manifest.Policies.File.SignatureURL = ""
			},
			wantErrSub: "requires policies.file.signatureUrl",
		},
		{
			name: "signed bundle missing public key lookup",
			mutate: func(manifest *NetworkManifest) {
				manifest.Policies.Source = PolicySourceBundle
				manifest.Policies.File = nil
				manifest.Policies.Bundle = &NetworkManifestBundle{
					ID:              "network-policy-bundle",
					URL:             "https://example.com/policy.tar.gz",
					PolicyQueryPath: "data.logistics.result",
					Signed:          true,
				}
			},
			wantErrSub: "requires policies.bundle.signingPublicKeyLookupUrl",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			manifest := validNetworkManifestForTest(expectedNetworkID, now)
			if tt.mutate != nil {
				tt.mutate(&manifest)
			}

			err := manifest.Validate(expectedNetworkID, now)
			if tt.wantErrSub == "" {
				if err != nil {
					t.Fatalf("expected success, got error: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tt.wantErrSub)
			}
			if !strings.Contains(err.Error(), tt.wantErrSub) {
				t.Fatalf("expected error containing %q, got %v", tt.wantErrSub, err)
			}
		})
	}
}

func validNodeManifestForTest(subscriberID string, now time.Time) NodeManifest {
	return NodeManifest{
		ManifestVersion: "1.0",
		ManifestType:    NodeManifestType,
		SubscriberID:    subscriberID,
		Schema: NodeManifestSchema{
			SchemaObjects: []SchemaObject{
				{
					Type:              "beckn:Order",
					BaseURL:           "https://schema.beckn.io/Order",
					SupportedVersions: []string{"2.0"},
				},
			},
		},
		Governance: NodeManifestGovernance{
			EffectiveFrom:  now.Add(-1 * time.Hour).Format(time.RFC3339),
			EffectiveUntil: now.Add(24 * time.Hour).Format(time.RFC3339),
		},
	}
}

func TestValidateNodeManifest(t *testing.T) {
	const subscriberID = "nfh.global/subscribers.beckn.one/marketplace.example.com"
	now := time.Now().UTC()

	tests := []struct {
		name       string
		mutate     func(*NodeManifest)
		wantErrSub string
	}{
		{
			name:       "valid manifest passes",
			mutate:     func(_ *NodeManifest) {},
			wantErrSub: "",
		},
		{
			name: "missing manifestVersion",
			mutate: func(m *NodeManifest) {
				m.ManifestVersion = ""
			},
			wantErrSub: "missing manifestVersion",
		},
		{
			name: "wrong manifestType",
			mutate: func(m *NodeManifest) {
				m.ManifestType = "network-manifest"
			},
			wantErrSub: `must have manifestType="node-manifest"`,
		},
		{
			name: "missing subscriberId",
			mutate: func(m *NodeManifest) {
				m.SubscriberID = ""
			},
			wantErrSub: "missing subscriberId",
		},
		{
			name: "subscriberId only two parts",
			mutate: func(m *NodeManifest) {
				m.SubscriberID = "nfh.global/subscribers.beckn.one"
			},
			wantErrSub: "namespace/registry/recordId format",
		},
		{
			name: "subscriberId has empty segment",
			mutate: func(m *NodeManifest) {
				m.SubscriberID = "nfh.global//marketplace.example.com"
			},
			wantErrSub: "namespace/registry/recordId format",
		},
		{
			name: "subscriberId mismatch",
			mutate: func(m *NodeManifest) {
				m.SubscriberID = "nfh.global/subscribers.beckn.one/other.example.com"
			},
			wantErrSub: "does not match expected",
		},
		{
			name: "empty schemaObjects",
			mutate: func(m *NodeManifest) {
				m.Schema.SchemaObjects = nil
			},
			wantErrSub: "at least one schema object",
		},
		{
			name: "schema object missing baseUrl",
			mutate: func(m *NodeManifest) {
				m.Schema.SchemaObjects = []SchemaObject{{Type: "beckn:Order", SupportedVersions: []string{"2.0"}}}
			},
			wantErrSub: "missing baseUrl",
		},
		{
			name: "schema object missing type",
			mutate: func(m *NodeManifest) {
				m.Schema.SchemaObjects = []SchemaObject{{BaseURL: "https://schema.beckn.io/Order", SupportedVersions: []string{"2.0"}}}
			},
			wantErrSub: "missing type",
		},
		{
			name: "schema object missing supportedVersions",
			mutate: func(m *NodeManifest) {
				m.Schema.SchemaObjects = []SchemaObject{{Type: "beckn:Order", BaseURL: "https://schema.beckn.io/Order"}}
			},
			wantErrSub: "no supportedVersions",
		},
		{
			name: "schema object unknown versionPolicy",
			mutate: func(m *NodeManifest) {
				m.Schema.SchemaObjects = []SchemaObject{{Type: "beckn:Order", BaseURL: "https://schema.beckn.io/Order", SupportedVersions: []string{"2.0"}, VersionPolicy: "newest"}}
			},
			wantErrSub: "unknown versionPolicy",
		},
		{
			name: "schema object pinned without pinnedVersion",
			mutate: func(m *NodeManifest) {
				m.Schema.SchemaObjects = []SchemaObject{{Type: "beckn:Order", BaseURL: "https://schema.beckn.io/Order", SupportedVersions: []string{"2.0"}, VersionPolicy: "pinned"}}
			},
			wantErrSub: "no pinnedVersion",
		},
		{
			name: "schema object pinnedVersion not in supportedVersions",
			mutate: func(m *NodeManifest) {
				m.Schema.SchemaObjects = []SchemaObject{{Type: "beckn:Order", BaseURL: "https://schema.beckn.io/Order", SupportedVersions: []string{"2.0"}, VersionPolicy: "pinned", PinnedVersion: "1.0"}}
			},
			wantErrSub: "not in supportedVersions",
		},
		{
			name: "invalid effectiveFrom",
			mutate: func(m *NodeManifest) {
				m.Governance.EffectiveFrom = "not-a-timestamp"
			},
			wantErrSub: "invalid governance.effectiveFrom",
		},
		{
			name: "effectiveFrom in the future",
			mutate: func(m *NodeManifest) {
				m.Governance.EffectiveFrom = now.Add(2 * time.Hour).Format(time.RFC3339)
			},
			wantErrSub: "is not active until",
		},
		{
			name: "invalid effectiveUntil",
			mutate: func(m *NodeManifest) {
				m.Governance.EffectiveUntil = "not-a-timestamp"
			},
			wantErrSub: "invalid governance.effectiveUntil",
		},
		{
			name: "effectiveUntil before effectiveFrom",
			mutate: func(m *NodeManifest) {
				m.Governance.EffectiveFrom = now.Add(-2 * time.Hour).Format(time.RFC3339)
				m.Governance.EffectiveUntil = now.Add(-3 * time.Hour).Format(time.RFC3339)
			},
			wantErrSub: "effectiveUntil later than governance.effectiveFrom",
		},
		{
			name: "expired manifest",
			mutate: func(m *NodeManifest) {
				m.Governance.EffectiveFrom = now.Add(-3 * time.Hour).Format(time.RFC3339)
				m.Governance.EffectiveUntil = now.Add(-1 * time.Hour).Format(time.RFC3339)
			},
			wantErrSub: "expired at",
		},
		{
			name: "no effectiveUntil is valid indefinite",
			mutate: func(m *NodeManifest) {
				m.Governance.EffectiveUntil = ""
			},
			wantErrSub: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			manifest := validNodeManifestForTest(subscriberID, now)
			tt.mutate(&manifest)
			err := manifest.Validate(subscriberID, now)
			if tt.wantErrSub == "" {
				if err != nil {
					t.Fatalf("expected no error, got %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErrSub) {
				t.Fatalf("expected error containing %q, got %v", tt.wantErrSub, err)
			}
		})
	}
}

func TestParseNodeManifest(t *testing.T) {
	yaml := `
manifestVersion: "1.0"
manifestType: "node-manifest"
subscriberId: "nfh.global/subscribers.beckn.one/marketplace.example.com"
schema:
  defaultVersionPolicy: "latest"
  schemaObjects:
    - type: "beckn:Order"
      baseUrl: "https://schema.beckn.io/Order"
      supportedVersions:
        - "2.0"
        - "2.1"
    - type: "beckn:Item"
      baseUrl: "https://schema.beckn.io/Item"
      supportedVersions:
        - "1.0"
      versionPolicy: "pinned"
      pinnedVersion: "1.0"
governance:
  effectiveFrom: "2026-01-01T00:00:00Z"
`
	m, err := ParseNodeManifest([]byte(yaml))
	if err != nil {
		t.Fatalf("ParseNodeManifest() error = %v", err)
	}
	if m.ManifestVersion != "1.0" {
		t.Errorf("expected manifestVersion 1.0, got %q", m.ManifestVersion)
	}
	if m.ManifestType != NodeManifestType {
		t.Errorf("expected manifestType %q, got %q", NodeManifestType, m.ManifestType)
	}
	if m.SubscriberID != "nfh.global/subscribers.beckn.one/marketplace.example.com" {
		t.Errorf("unexpected subscriberId: %q", m.SubscriberID)
	}
	if m.Schema.DefaultVersionPolicy != "latest" {
		t.Errorf("unexpected defaultVersionPolicy: %q", m.Schema.DefaultVersionPolicy)
	}
	if len(m.Schema.SchemaObjects) != 2 {
		t.Fatalf("expected 2 schema objects, got %d", len(m.Schema.SchemaObjects))
	}
	obj0 := m.Schema.SchemaObjects[0]
	if obj0.Type != "beckn:Order" {
		t.Errorf("unexpected type: %q", obj0.Type)
	}
	if obj0.BaseURL != "https://schema.beckn.io/Order" {
		t.Errorf("unexpected baseUrl: %q", obj0.BaseURL)
	}
	if len(obj0.SupportedVersions) != 2 || obj0.SupportedVersions[0] != "2.0" || obj0.SupportedVersions[1] != "2.1" {
		t.Errorf("unexpected supportedVersions: %v", obj0.SupportedVersions)
	}
	obj1 := m.Schema.SchemaObjects[1]
	if obj1.VersionPolicy != "pinned" || obj1.PinnedVersion != "1.0" {
		t.Errorf("unexpected pinned policy: policy=%q pinnedVersion=%q", obj1.VersionPolicy, obj1.PinnedVersion)
	}
	if m.Governance.EffectiveFrom != "2026-01-01T00:00:00Z" {
		t.Errorf("unexpected effectiveFrom: %q", m.Governance.EffectiveFrom)
	}
	if m.Governance.EffectiveUntil != "" {
		t.Errorf("expected empty effectiveUntil, got %q", m.Governance.EffectiveUntil)
	}
}

func TestParseNodeManifest_InvalidYAML(t *testing.T) {
	_, err := ParseNodeManifest([]byte("{{not valid yaml"))
	if err == nil {
		t.Fatal("expected error for invalid YAML, got nil")
	}
}

