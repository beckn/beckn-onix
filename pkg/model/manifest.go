// Network manifest schema types and validation. gopkg.in/yaml.v3 is imported here
// because ParseNetworkManifest is the canonical constructor for NetworkManifest.
package model

import (
	"fmt"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// ManifestMetadata describes the three inputs needed to fetch and verify a manifest.
type ManifestMetadata struct {
	ManifestURL               string
	ManifestSignatureURL      string
	SigningPublicKeyLookupURL string
}

// ManifestDocument is the cached and returned verified manifest payload.
type ManifestDocument struct {
	NetworkID    string    `json:"network_id,omitempty"`
	ContentType  string    `json:"content_type,omitempty"`
	Content      []byte    `json:"content"`
	Digest       string    `json:"digest"`
	SourceURL    string    `json:"source_url"`
	SignatureURL string    `json:"signature_url"`
	Verified     bool      `json:"verified"`
	FetchedAt    time.Time `json:"fetched_at"`
}

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
	ID                       string `yaml:"id"`
	URL                      string `yaml:"url"`
	PolicyQueryPath          string `yaml:"policy_query_path"`
	Signed                   bool   `yaml:"signed"`
	SigningPublicKeyLookupURL string `yaml:"signing_public_key_lookup_url"`
}

// NetworkManifestFile describes a single Rego policy artifact.
type NetworkManifestFile struct {
	ID                       string `yaml:"id"`
	URL                      string `yaml:"url"`
	PolicyQueryPath          string `yaml:"policy_query_path"`
	Signed                   bool   `yaml:"signed"`
	SignatureURL             string `yaml:"signature_url"`
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

// Validate checks the manifest against shared schema rules for the given network ID and time.
func (m *NetworkManifest) Validate(expectedNetworkID string, now time.Time) error {
	if m == nil {
		return fmt.Errorf("manifest for network %q cannot be nil", expectedNetworkID)
	}
	if strings.TrimSpace(m.ManifestVersion) == "" {
		return fmt.Errorf("manifest for network %q is missing manifest_version", expectedNetworkID)
	}
	if m.ManifestType != NetworkManifestType {
		return fmt.Errorf("manifest for network %q must have manifest_type=\"network-manifest\"", expectedNetworkID)
	}
	if m.NetworkID == "" {
		return fmt.Errorf("manifest for network %q is missing network_id", expectedNetworkID)
	}
	if m.NetworkID != expectedNetworkID {
		return fmt.Errorf("manifest network_id %q does not match configured network %q", m.NetworkID, expectedNetworkID)
	}
	if m.ReleaseID == nil || strings.TrimSpace(fmt.Sprintf("%v", m.ReleaseID)) == "" {
		return fmt.Errorf("manifest for network %q is missing release_id", expectedNetworkID)
	}
	if strings.TrimSpace(m.Publisher.Role) == "" || strings.TrimSpace(m.Publisher.Domain) == "" {
		return fmt.Errorf("manifest for network %q must include publisher.role and publisher.domain", expectedNetworkID)
	}
	if m.Policies == nil {
		return fmt.Errorf("manifest for network %q is missing policies section", expectedNetworkID)
	}
	if m.Policies.Type != PolicyTypeRego {
		return fmt.Errorf("manifest for network %q must have policies.type=\"rego\"", expectedNetworkID)
	}
	if m.Governance.Signed == nil {
		return fmt.Errorf("manifest for network %q is missing governance.signed", expectedNetworkID)
	}

	effectiveFrom, err := time.Parse(time.RFC3339, m.Governance.EffectiveFrom)
	if err != nil {
		return fmt.Errorf("manifest for network %q has invalid governance.effective_from: %w", expectedNetworkID, err)
	}
	if now.Before(effectiveFrom) {
		return fmt.Errorf("manifest for network %q is not active until %s", expectedNetworkID, effectiveFrom.Format(time.RFC3339))
	}

	if m.Governance.EffectiveUntil != "" {
		effectiveUntil, err := time.Parse(time.RFC3339, m.Governance.EffectiveUntil)
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

	switch m.Policies.Source {
	case PolicySourceBundle:
		if m.Policies.Bundle == nil {
			return fmt.Errorf("manifest for network %q must include policies.bundle when policies.source=\"bundle\"", expectedNetworkID)
		}
		if m.Policies.File != nil {
			return fmt.Errorf("manifest for network %q must not include policies.file when policies.source=\"bundle\"", expectedNetworkID)
		}
		if strings.TrimSpace(m.Policies.Bundle.ID) == "" ||
			strings.TrimSpace(m.Policies.Bundle.URL) == "" ||
			strings.TrimSpace(m.Policies.Bundle.PolicyQueryPath) == "" {
			return fmt.Errorf("manifest for network %q is missing required policies.bundle fields", expectedNetworkID)
		}
		if m.Policies.Bundle.Signed && strings.TrimSpace(m.Policies.Bundle.SigningPublicKeyLookupURL) == "" {
			return fmt.Errorf("manifest for network %q requires policies.bundle.signing_public_key_lookup_url when policies.bundle.signed=true", expectedNetworkID)
		}
	case PolicySourceFile:
		if m.Policies.File == nil {
			return fmt.Errorf("manifest for network %q must include policies.file when policies.source=\"file\"", expectedNetworkID)
		}
		if m.Policies.Bundle != nil {
			return fmt.Errorf("manifest for network %q must not include policies.bundle when policies.source=\"file\"", expectedNetworkID)
		}
		if strings.TrimSpace(m.Policies.File.ID) == "" ||
			strings.TrimSpace(m.Policies.File.URL) == "" ||
			strings.TrimSpace(m.Policies.File.PolicyQueryPath) == "" {
			return fmt.Errorf("manifest for network %q is missing required policies.file fields", expectedNetworkID)
		}
		if m.Policies.File.Signed {
			if strings.TrimSpace(m.Policies.File.SignatureURL) == "" {
				return fmt.Errorf("manifest for network %q requires policies.file.signature_url when policies.file.signed=true", expectedNetworkID)
			}
			if strings.TrimSpace(m.Policies.File.SigningPublicKeyLookupURL) == "" {
				return fmt.Errorf("manifest for network %q requires policies.file.signing_public_key_lookup_url when policies.file.signed=true", expectedNetworkID)
			}
		}
	default:
		return fmt.Errorf("manifest for network %q uses unsupported policies.source %q", expectedNetworkID, m.Policies.Source)
	}

	return nil
}
