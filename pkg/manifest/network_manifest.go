package manifest

import (
	"fmt"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

const (
	// NetworkManifestType is the manifest_type value for network manifests.
	NetworkManifestType = "network-manifest"
	// PolicyTypeRego is the policies.type value for Rego policy manifests.
	PolicyTypeRego = "rego"
	// PolicySourceBundle is the policies.source value for OPA bundle policies.
	PolicySourceBundle = "bundle"
	// PolicySourceFile is the policies.source value for single Rego file policies.
	PolicySourceFile = "file"
)

// NetworkManifest is the typed YAML schema for a network-manifest document.
type NetworkManifest struct {
	ManifestVersion string                    `yaml:"manifest_version"`
	ManifestType    string                    `yaml:"manifest_type"`
	NetworkID       string                    `yaml:"network_id"`
	ReleaseID       any                       `yaml:"release_id"`
	Publisher       NetworkManifestPublisher  `yaml:"publisher"`
	Policies        *NetworkManifestPolicies  `yaml:"policies"`
	Governance      NetworkManifestGovernance `yaml:"governance"`
}

// NetworkManifestPublisher identifies the organization publishing the manifest.
type NetworkManifestPublisher struct {
	Role   string `yaml:"role"`
	Domain string `yaml:"domain"`
}

// NetworkManifestPolicies describes the policy artifact referenced by a network manifest.
type NetworkManifestPolicies struct {
	Type   string                 `yaml:"type"`
	Source string                 `yaml:"source"`
	Bundle *NetworkManifestBundle `yaml:"bundle"`
	File   *NetworkManifestFile   `yaml:"file"`
}

// NetworkManifestBundle describes an OPA bundle policy artifact.
type NetworkManifestBundle struct {
	ID                        string `yaml:"id"`
	URL                       string `yaml:"url"`
	PolicyQueryPath           string `yaml:"policy_query_path"`
	Signed                    bool   `yaml:"signed"`
	SigningPublicKeyLookupURL string `yaml:"signing_public_key_lookup_url"`
}

// NetworkManifestFile describes a single Rego policy artifact.
type NetworkManifestFile struct {
	ID                        string `yaml:"id"`
	URL                       string `yaml:"url"`
	PolicyQueryPath           string `yaml:"policy_query_path"`
	Signed                    bool   `yaml:"signed"`
	SignatureURL              string `yaml:"signature_url"`
	SigningPublicKeyLookupURL string `yaml:"signing_public_key_lookup_url"`
}

// NetworkManifestGovernance describes validity and signature metadata.
type NetworkManifestGovernance struct {
	EffectiveFrom  string `yaml:"effective_from"`
	EffectiveUntil string `yaml:"effective_until"`
	Signed         *bool  `yaml:"signed"`
}

// ParseNetworkManifest parses YAML network manifest content.
func ParseNetworkManifest(content []byte) (*NetworkManifest, error) {
	var manifest NetworkManifest
	if err := yaml.Unmarshal(content, &manifest); err != nil {
		return nil, err
	}
	return &manifest, nil
}

// ValidateNetworkManifest validates a network manifest against shared schema rules.
func ValidateNetworkManifest(manifest *NetworkManifest, expectedNetworkID string, now time.Time) error {
	if manifest == nil {
		return fmt.Errorf("manifest for network %q cannot be nil", expectedNetworkID)
	}
	if strings.TrimSpace(manifest.ManifestVersion) == "" {
		return fmt.Errorf("manifest for network %q is missing manifest_version", expectedNetworkID)
	}
	if manifest.ManifestType != NetworkManifestType {
		return fmt.Errorf("manifest for network %q must have manifest_type=\"network-manifest\"", expectedNetworkID)
	}
	if manifest.NetworkID == "" {
		return fmt.Errorf("manifest for network %q is missing network_id", expectedNetworkID)
	}
	if manifest.NetworkID != expectedNetworkID {
		return fmt.Errorf("manifest network_id %q does not match configured network %q", manifest.NetworkID, expectedNetworkID)
	}
	if manifest.ReleaseID == nil || strings.TrimSpace(fmt.Sprintf("%v", manifest.ReleaseID)) == "" {
		return fmt.Errorf("manifest for network %q is missing release_id", expectedNetworkID)
	}
	if strings.TrimSpace(manifest.Publisher.Role) == "" || strings.TrimSpace(manifest.Publisher.Domain) == "" {
		return fmt.Errorf("manifest for network %q must include publisher.role and publisher.domain", expectedNetworkID)
	}
	if manifest.Policies == nil {
		return fmt.Errorf("manifest for network %q is missing policies section", expectedNetworkID)
	}
	if manifest.Policies.Type != PolicyTypeRego {
		return fmt.Errorf("manifest for network %q must have policies.type=\"rego\"", expectedNetworkID)
	}
	if manifest.Governance.Signed == nil {
		return fmt.Errorf("manifest for network %q is missing governance.signed", expectedNetworkID)
	}

	effectiveFrom, err := time.Parse(time.RFC3339, manifest.Governance.EffectiveFrom)
	if err != nil {
		return fmt.Errorf("manifest for network %q has invalid governance.effective_from: %w", expectedNetworkID, err)
	}
	if now.Before(effectiveFrom) {
		return fmt.Errorf("manifest for network %q is not active until %s", expectedNetworkID, effectiveFrom.Format(time.RFC3339))
	}

	if manifest.Governance.EffectiveUntil != "" {
		effectiveUntil, err := time.Parse(time.RFC3339, manifest.Governance.EffectiveUntil)
		if err != nil {
			return fmt.Errorf("manifest for network %q has invalid governance.effective_until: %w", expectedNetworkID, err)
		}
		if !effectiveUntil.After(effectiveFrom) {
			return fmt.Errorf("manifest for network %q must have governance.effective_until later than governance.effective_from", expectedNetworkID)
		}
		if now.After(effectiveUntil) {
			return fmt.Errorf("manifest for network %q expired at %s", expectedNetworkID, effectiveUntil.Format(time.RFC3339))
		}
	}

	switch manifest.Policies.Source {
	case PolicySourceBundle:
		if manifest.Policies.Bundle == nil {
			return fmt.Errorf("manifest for network %q must include policies.bundle when policies.source=\"bundle\"", expectedNetworkID)
		}
		if manifest.Policies.File != nil {
			return fmt.Errorf("manifest for network %q must not include policies.file when policies.source=\"bundle\"", expectedNetworkID)
		}
		if strings.TrimSpace(manifest.Policies.Bundle.ID) == "" ||
			strings.TrimSpace(manifest.Policies.Bundle.URL) == "" ||
			strings.TrimSpace(manifest.Policies.Bundle.PolicyQueryPath) == "" {
			return fmt.Errorf("manifest for network %q is missing required policies.bundle fields", expectedNetworkID)
		}
		if manifest.Policies.Bundle.Signed && strings.TrimSpace(manifest.Policies.Bundle.SigningPublicKeyLookupURL) == "" {
			return fmt.Errorf("manifest for network %q requires policies.bundle.signing_public_key_lookup_url when policies.bundle.signed=true", expectedNetworkID)
		}
	case PolicySourceFile:
		if manifest.Policies.File == nil {
			return fmt.Errorf("manifest for network %q must include policies.file when policies.source=\"file\"", expectedNetworkID)
		}
		if manifest.Policies.Bundle != nil {
			return fmt.Errorf("manifest for network %q must not include policies.bundle when policies.source=\"file\"", expectedNetworkID)
		}
		if strings.TrimSpace(manifest.Policies.File.ID) == "" ||
			strings.TrimSpace(manifest.Policies.File.URL) == "" ||
			strings.TrimSpace(manifest.Policies.File.PolicyQueryPath) == "" {
			return fmt.Errorf("manifest for network %q is missing required policies.file fields", expectedNetworkID)
		}
		if manifest.Policies.File.Signed {
			if strings.TrimSpace(manifest.Policies.File.SignatureURL) == "" {
				return fmt.Errorf("manifest for network %q requires policies.file.signature_url when policies.file.signed=true", expectedNetworkID)
			}
			if strings.TrimSpace(manifest.Policies.File.SigningPublicKeyLookupURL) == "" {
				return fmt.Errorf("manifest for network %q requires policies.file.signing_public_key_lookup_url when policies.file.signed=true", expectedNetworkID)
			}
		}
	default:
		return fmt.Errorf("manifest for network %q uses unsupported policies.source %q", expectedNetworkID, manifest.Policies.Source)
	}

	return nil
}
