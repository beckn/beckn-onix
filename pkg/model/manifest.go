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
	ManifestVersion string                        `yaml:"manifestVersion"`
	ManifestType    string                        `yaml:"manifestType"`
	NetworkID       string                        `yaml:"networkId"`
	ReleaseID       any                           `yaml:"releaseId"`
	Publisher       NetworkManifestPublisher      `yaml:"publisher"`
	Policies        *NetworkManifestPolicies      `yaml:"policies"`
	Deployment      *NetworkManifestDeployment    `yaml:"deployment"`
	Observability   *NetworkManifestObservability `yaml:"observability"`
	Governance      NetworkManifestGovernance     `yaml:"governance"`
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
	PolicyQueryPath           string `yaml:"policyQueryPath"`
	Signed                    bool   `yaml:"signed"`
	SigningPublicKeyLookupURL string `yaml:"signingPublicKeyLookupUrl"`
}

// NetworkManifestFile describes a single Rego policy artifact.
type NetworkManifestFile struct {
	ID                        string `yaml:"id"`
	URL                       string `yaml:"url"`
	PolicyQueryPath           string `yaml:"policyQueryPath"`
	Signed                    bool   `yaml:"signed"`
	SignatureURL              string `yaml:"signatureUrl"`
	SigningPublicKeyLookupURL string `yaml:"signingPublicKeyLookupUrl"`
}

// NetworkManifestArtifactRef references a single published artifact that may
// carry a detached signature. It is the shared shape for non-policy artifacts
// referenced from a manifest (deployment baselines, observability configs).
type NetworkManifestArtifactRef struct {
	ID                        string `yaml:"id"`
	URL                       string `yaml:"url"`
	Signed                    bool   `yaml:"signed"`
	SignatureURL              string `yaml:"signatureUrl"`
	SigningPublicKeyLookupURL string `yaml:"signingPublicKeyLookupUrl"`
}

// NetworkManifestDeployment describes the deployment-conformance release for a
// network: the signed baseline document participants verify their deployed
// configuration against, and an optional Rego policy evaluated over the
// discovered configuration tree.
type NetworkManifestDeployment struct {
	DevkitID string                      `yaml:"devkitId"`
	Baseline *NetworkManifestArtifactRef `yaml:"baseline"`
	Policy   *NetworkManifestPolicies    `yaml:"policy"`
}

// NetworkManifestObservability describes network-level telemetry settings: an
// optional signed observability config document and the collector endpoint
// that receives network telemetry events.
type NetworkManifestObservability struct {
	Enabled   bool                        `yaml:"enabled"`
	Config    *NetworkManifestArtifactRef `yaml:"config"`
	Collector *NetworkManifestCollector   `yaml:"collector"`
}

// NetworkManifestCollector identifies the endpoint receiving telemetry events.
type NetworkManifestCollector struct {
	URL string `yaml:"url"`
}

// NetworkManifestGovernance describes validity and signature metadata.
type NetworkManifestGovernance struct {
	EffectiveFrom  string `yaml:"effectiveFrom"`
	EffectiveUntil string `yaml:"effectiveUntil"`
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
		return fmt.Errorf("manifest for network %q is missing manifestVersion", expectedNetworkID)
	}
	if m.ManifestType != NetworkManifestType {
		return fmt.Errorf("manifest for network %q must have manifestType=\"network-manifest\"", expectedNetworkID)
	}
	if m.NetworkID == "" {
		return fmt.Errorf("manifest for network %q is missing networkId", expectedNetworkID)
	}
	if m.NetworkID != expectedNetworkID {
		return fmt.Errorf("manifest networkId %q does not match configured network %q", m.NetworkID, expectedNetworkID)
	}
	if m.ReleaseID == nil || strings.TrimSpace(fmt.Sprintf("%v", m.ReleaseID)) == "" {
		return fmt.Errorf("manifest for network %q is missing releaseId", expectedNetworkID)
	}
	if strings.TrimSpace(m.Publisher.Role) == "" || strings.TrimSpace(m.Publisher.Domain) == "" {
		return fmt.Errorf("manifest for network %q must include publisher.role and publisher.domain", expectedNetworkID)
	}
	if m.Policies == nil {
		return fmt.Errorf("manifest for network %q is missing policies section", expectedNetworkID)
	}
	if err := validatePolicySection(m.Policies, expectedNetworkID, "policies"); err != nil {
		return err
	}
	if err := validateDeploymentSection(m.Deployment, expectedNetworkID); err != nil {
		return err
	}
	if err := validateObservabilitySection(m.Observability, expectedNetworkID); err != nil {
		return err
	}
	if m.Governance.Signed == nil {
		return fmt.Errorf("manifest for network %q is missing governance.signed", expectedNetworkID)
	}

	effectiveFrom, err := time.Parse(time.RFC3339, m.Governance.EffectiveFrom)
	if err != nil {
		return fmt.Errorf("manifest for network %q has invalid governance.effectiveFrom: %w", expectedNetworkID, err)
	}
	if now.Before(effectiveFrom) {
		return fmt.Errorf("manifest for network %q is not active until %s", expectedNetworkID, effectiveFrom.Format(time.RFC3339))
	}

	if m.Governance.EffectiveUntil != "" {
		effectiveUntil, err := time.Parse(time.RFC3339, m.Governance.EffectiveUntil)
		if err != nil {
			return fmt.Errorf("manifest for network %q has invalid governance.effectiveUntil: %w", expectedNetworkID, err)
		}
		if !effectiveUntil.After(effectiveFrom) {
			return fmt.Errorf("manifest for network %q must have governance.effectiveUntil later than governance.effectiveFrom", expectedNetworkID)
		}
		if now.After(effectiveUntil) {
			return fmt.Errorf("manifest for network %q expired at %s", expectedNetworkID, effectiveUntil.Format(time.RFC3339))
		}
	}

	return nil
}

// validatePolicySection checks the type/source/bundle/file invariants of a
// policies-shaped section. section names the YAML location being validated
// ("policies" or "deployment.policy") so errors point at the exact field.
func validatePolicySection(p *NetworkManifestPolicies, networkID, section string) error {
	if p.Type != PolicyTypeRego {
		return fmt.Errorf("manifest for network %q must have %s.type=\"rego\"", networkID, section)
	}
	switch p.Source {
	case PolicySourceBundle:
		if p.Bundle == nil {
			return fmt.Errorf("manifest for network %q must include %s.bundle when %s.source=\"bundle\"", networkID, section, section)
		}
		if p.File != nil {
			return fmt.Errorf("manifest for network %q must not include %s.file when %s.source=\"bundle\"", networkID, section, section)
		}
		if strings.TrimSpace(p.Bundle.ID) == "" ||
			strings.TrimSpace(p.Bundle.URL) == "" ||
			strings.TrimSpace(p.Bundle.PolicyQueryPath) == "" {
			return fmt.Errorf("manifest for network %q is missing required %s.bundle fields", networkID, section)
		}
		if p.Bundle.Signed && strings.TrimSpace(p.Bundle.SigningPublicKeyLookupURL) == "" {
			return fmt.Errorf("manifest for network %q requires %s.bundle.signingPublicKeyLookupUrl when %s.bundle.signed=true", networkID, section, section)
		}
	case PolicySourceFile:
		if p.File == nil {
			return fmt.Errorf("manifest for network %q must include %s.file when %s.source=\"file\"", networkID, section, section)
		}
		if p.Bundle != nil {
			return fmt.Errorf("manifest for network %q must not include %s.bundle when %s.source=\"file\"", networkID, section, section)
		}
		if strings.TrimSpace(p.File.ID) == "" ||
			strings.TrimSpace(p.File.URL) == "" ||
			strings.TrimSpace(p.File.PolicyQueryPath) == "" {
			return fmt.Errorf("manifest for network %q is missing required %s.file fields", networkID, section)
		}
		if p.File.Signed {
			if strings.TrimSpace(p.File.SignatureURL) == "" {
				return fmt.Errorf("manifest for network %q requires %s.file.signatureUrl when %s.file.signed=true", networkID, section, section)
			}
			if strings.TrimSpace(p.File.SigningPublicKeyLookupURL) == "" {
				return fmt.Errorf("manifest for network %q requires %s.file.signingPublicKeyLookupUrl when %s.file.signed=true", networkID, section, section)
			}
		}
	default:
		return fmt.Errorf("manifest for network %q uses unsupported %s.source %q", networkID, section, p.Source)
	}
	return nil
}

// validateArtifactRef checks that an artifact reference names a URL and, when
// marked signed, carries the detached-signature and key-lookup URLs needed to
// verify it. section names the YAML location for error messages.
func validateArtifactRef(ref *NetworkManifestArtifactRef, networkID, section string) error {
	if strings.TrimSpace(ref.ID) == "" || strings.TrimSpace(ref.URL) == "" {
		return fmt.Errorf("manifest for network %q is missing required %s.id or %s.url", networkID, section, section)
	}
	if ref.Signed {
		if strings.TrimSpace(ref.SignatureURL) == "" {
			return fmt.Errorf("manifest for network %q requires %s.signatureUrl when %s.signed=true", networkID, section, section)
		}
		if strings.TrimSpace(ref.SigningPublicKeyLookupURL) == "" {
			return fmt.Errorf("manifest for network %q requires %s.signingPublicKeyLookupUrl when %s.signed=true", networkID, section, section)
		}
	}
	return nil
}

// validateDeploymentSection checks the optional deployment section: a devkit
// identifier and baseline reference are required, the config-tree policy is
// optional and shares the policies-section schema.
func validateDeploymentSection(d *NetworkManifestDeployment, networkID string) error {
	if d == nil {
		return nil
	}
	if strings.TrimSpace(d.DevkitID) == "" {
		return fmt.Errorf("manifest for network %q is missing deployment.devkitId", networkID)
	}
	if d.Baseline == nil {
		return fmt.Errorf("manifest for network %q is missing deployment.baseline", networkID)
	}
	if err := validateArtifactRef(d.Baseline, networkID, "deployment.baseline"); err != nil {
		return err
	}
	if d.Policy != nil {
		return validatePolicySection(d.Policy, networkID, "deployment.policy")
	}
	return nil
}

// validateObservabilitySection checks the optional observability section: when
// enabled, any config document reference must be well formed and a collector,
// if declared, must name its endpoint URL.
func validateObservabilitySection(o *NetworkManifestObservability, networkID string) error {
	if o == nil {
		return nil
	}
	if o.Config != nil {
		if err := validateArtifactRef(o.Config, networkID, "observability.config"); err != nil {
			return err
		}
	}
	if o.Collector != nil && strings.TrimSpace(o.Collector.URL) == "" {
		return fmt.Errorf("manifest for network %q is missing observability.collector.url", networkID)
	}
	return nil
}
