package manifest

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
				ID:                        "network-policy-file",
				URL:                       "https://example.com/policy.rego",
				PolicyQueryPath:           "data.logistics.result",
				Signed:                    true,
				SignatureURL:              "https://example.com/policy.rego.sig",
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
			wantErrSub: `must have manifest_type="network-manifest"`,
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
			wantErrSub: "invalid governance.effective_until",
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
			wantErrSub: "requires policies.file.signature_url",
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
			wantErrSub: "requires policies.bundle.signing_public_key_lookup_url",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			manifest := validNetworkManifestForTest(expectedNetworkID, now)
			if tt.mutate != nil {
				tt.mutate(&manifest)
			}

			err := ValidateNetworkManifest(&manifest, expectedNetworkID, now)
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
