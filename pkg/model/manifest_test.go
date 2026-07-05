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

// validDeploymentForTest returns a deployment section that passes validation,
// for mutation-based failure cases.
func validDeploymentForTest() *NetworkManifestDeployment {
	return &NetworkManifestDeployment{
		DevkitID: "p2p-trading-devkit",
		Baseline: &NetworkManifestArtifactRef{
			ID:                        "deployment-baseline-v1",
			URL:                       "https://example.com/baseline.yaml",
			Signed:                    true,
			SignatureURL:              "https://example.com/baseline.yaml.sig",
			SigningPublicKeyLookupURL: "https://example.com/public-key",
		},
	}
}

// validObservabilityForTest returns an observability section that passes
// validation, for mutation-based failure cases.
func validObservabilityForTest() *NetworkManifestObservability {
	return &NetworkManifestObservability{
		Enabled: true,
		Config: &NetworkManifestArtifactRef{
			ID:                        "observability-fields-v1",
			URL:                       "https://example.com/fields.yaml",
			Signed:                    true,
			SignatureURL:              "https://example.com/fields.yaml.sig",
			SigningPublicKeyLookupURL: "https://example.com/public-key",
		},
		Collector: &NetworkManifestCollector{URL: "https://telemetry.example.com/v1/network/events"},
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
		{
			name: "valid deployment section",
			mutate: func(manifest *NetworkManifest) {
				manifest.Deployment = validDeploymentForTest()
			},
		},
		{
			name: "deployment missing devkit id",
			mutate: func(manifest *NetworkManifest) {
				manifest.Deployment = validDeploymentForTest()
				manifest.Deployment.DevkitID = " "
			},
			wantErrSub: "missing deployment.devkitId",
		},
		{
			name: "deployment missing baseline",
			mutate: func(manifest *NetworkManifest) {
				manifest.Deployment = validDeploymentForTest()
				manifest.Deployment.Baseline = nil
			},
			wantErrSub: "missing deployment.baseline",
		},
		{
			name: "deployment signed baseline missing signature url",
			mutate: func(manifest *NetworkManifest) {
				manifest.Deployment = validDeploymentForTest()
				manifest.Deployment.Baseline.SignatureURL = ""
			},
			wantErrSub: "requires deployment.baseline.signatureUrl",
		},
		{
			name: "deployment policy with unsupported source",
			mutate: func(manifest *NetworkManifest) {
				manifest.Deployment = validDeploymentForTest()
				manifest.Deployment.Policy = &NetworkManifestPolicies{
					Type:   PolicyTypeRego,
					Source: "archive",
				}
			},
			wantErrSub: `unsupported deployment.policy.source "archive"`,
		},
		{
			name: "deployment policy file missing query path",
			mutate: func(manifest *NetworkManifest) {
				manifest.Deployment = validDeploymentForTest()
				manifest.Deployment.Policy = &NetworkManifestPolicies{
					Type:   PolicyTypeRego,
					Source: PolicySourceFile,
					File: &NetworkManifestFile{
						ID:  "deployment-policy",
						URL: "https://example.com/deployment.rego",
					},
				}
			},
			wantErrSub: "missing required deployment.policy.file fields",
		},
		{
			name: "valid observability section",
			mutate: func(manifest *NetworkManifest) {
				manifest.Observability = validObservabilityForTest()
			},
		},
		{
			name: "observability signed config missing key lookup",
			mutate: func(manifest *NetworkManifest) {
				manifest.Observability = validObservabilityForTest()
				manifest.Observability.Config.SigningPublicKeyLookupURL = ""
			},
			wantErrSub: "requires observability.config.signingPublicKeyLookupUrl",
		},
		{
			name: "observability collector missing url",
			mutate: func(manifest *NetworkManifest) {
				manifest.Observability = validObservabilityForTest()
				manifest.Observability.Collector.URL = " "
			},
			wantErrSub: "missing observability.collector.url",
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
