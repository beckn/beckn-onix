// Network manifest schema types and validation. gopkg.in/yaml.v3 is imported here
// because ParseNetworkManifest is the canonical constructor for NetworkManifest.
package model

import (
	"fmt"
	"strconv"
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
	SubscriberID string    `json:"subscriber_id,omitempty"`
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
	// NodeManifestType is the manifest_type value for node manifests.
	NodeManifestType = "node-manifest"
	// PolicyTypeRego is the policies.type value for Rego policy manifests.
	PolicyTypeRego = "rego"
	// PolicySourceBundle is the policies.source value for OPA bundle policies.
	PolicySourceBundle = "bundle"
	// PolicySourceFile is the policies.source value for single Rego file policies.
	PolicySourceFile = "file"
)

// NetworkManifest is the typed YAML schema for a network-manifest document.
type NetworkManifest struct {
	ManifestVersion string                    `yaml:"manifestVersion"`
	ManifestType    string                    `yaml:"manifestType"`
	NetworkID       string                    `yaml:"networkId"`
	ReleaseID       any                       `yaml:"releaseId"`
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
	PolicyQueryPath          string `yaml:"policyQueryPath"`
	Signed                   bool   `yaml:"signed"`
	SigningPublicKeyLookupURL string `yaml:"signingPublicKeyLookupUrl"`
}

// NetworkManifestFile describes a single Rego policy artifact.
type NetworkManifestFile struct {
	ID                       string `yaml:"id"`
	URL                      string `yaml:"url"`
	PolicyQueryPath          string `yaml:"policyQueryPath"`
	Signed                   bool   `yaml:"signed"`
	SignatureURL             string `yaml:"signatureUrl"`
	SigningPublicKeyLookupURL string `yaml:"signingPublicKeyLookupUrl"`
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
	if m.Policies.Type != PolicyTypeRego {
		return fmt.Errorf("manifest for network %q must have policies.type=\"rego\"", expectedNetworkID)
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
			return fmt.Errorf("manifest for network %q requires policies.bundle.signingPublicKeyLookupUrl when policies.bundle.signed=true", expectedNetworkID)
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
				return fmt.Errorf("manifest for network %q requires policies.file.signatureUrl when policies.file.signed=true", expectedNetworkID)
			}
			if strings.TrimSpace(m.Policies.File.SigningPublicKeyLookupURL) == "" {
				return fmt.Errorf("manifest for network %q requires policies.file.signingPublicKeyLookupUrl when policies.file.signed=true", expectedNetworkID)
			}
		}
	default:
		return fmt.Errorf("manifest for network %q uses unsupported policies.source %q", expectedNetworkID, m.Policies.Source)
	}

	return nil
}

// --- Node manifest types ---

const (
	// VersionPolicyLatest resolves to the highest version in SupportedVersions.
	VersionPolicyLatest = "latest"
	// VersionPolicyPinned resolves to the explicit PinnedVersion field.
	VersionPolicyPinned = "pinned"
)

// SchemaObject declares the schema types a node supports, with all accepted versions.
// BaseURL is the base URL prefix shared by all versions (e.g. "https://.../schema/RetailOffer").
// SupportedVersions lists every version the app handles natively — payloads at any of
// these versions are considered compatible without translation.
// VersionPolicy controls which version is the canonical translation target:
//   - "latest" (default) — highest version in SupportedVersions
//   - "pinned" — the explicit PinnedVersion value
//
// Wire format convention: {BaseURL}/{version}/context.jsonld
type SchemaObject struct {
	Type              string   `yaml:"type"`
	BaseURL           string   `yaml:"baseUrl"`
	SupportedVersions []string `yaml:"supportedVersions"`
	VersionPolicy     string   `yaml:"versionPolicy,omitempty"`
	PinnedVersion     string   `yaml:"pinnedVersion,omitempty"`
}

// CanonicalVersion resolves the preferred version for this schema object.
// defaultPolicy is used when the object's own VersionPolicy is empty; pass
// manifest.Schema.DefaultVersionPolicy. Falls back to VersionPolicyLatest.
func (s *SchemaObject) CanonicalVersion(defaultPolicy string) (string, error) {
	if len(s.SupportedVersions) == 0 {
		return "", fmt.Errorf("schema object %q has no supported versions", s.Type)
	}
	policy := s.VersionPolicy
	if policy == "" {
		policy = defaultPolicy
	}
	if policy == "" {
		policy = VersionPolicyLatest
	}
	switch policy {
	case VersionPolicyLatest:
		return latestVersion(s.SupportedVersions)
	case VersionPolicyPinned:
		if s.PinnedVersion == "" {
			return "", fmt.Errorf("schema object %q has versionPolicy=%q but no pinnedVersion", s.Type, VersionPolicyPinned)
		}
		return s.PinnedVersion, nil
	default:
		return "", fmt.Errorf("schema object %q has unknown versionPolicy %q", s.Type, policy)
	}
}

// latestVersion returns the highest version from the list using major.minor numeric comparison.
func latestVersion(versions []string) (string, error) {
	if len(versions) == 0 {
		return "", fmt.Errorf("empty versions list")
	}
	best := versions[0]
	for _, v := range versions[1:] {
		if compareVersion(v, best) > 0 {
			best = v
		}
	}
	return best, nil
}

// compareVersion compares two version strings (e.g. "v2.1", "v2.10").
// Returns positive when a > b, negative when a < b, zero when equal.
// Strips an optional leading "v"/"V" before parsing major.minor as integers.
func compareVersion(a, b string) int {
	parse := func(s string) (int, int) {
		if len(s) > 0 && (s[0] == 'v' || s[0] == 'V') {
			s = s[1:]
		}
		parts := strings.SplitN(s, ".", 2)
		major, _ := strconv.Atoi(parts[0])
		minor := 0
		if len(parts) > 1 {
			minor, _ = strconv.Atoi(parts[1])
		}
		return major, minor
	}
	aM, am := parse(a)
	bM, bm := parse(b)
	if aM != bM {
		return aM - bM
	}
	return am - bm
}

// NodeManifestSchema holds the schema capability declarations for a node manifest.
type NodeManifestSchema struct {
	DefaultVersionPolicy string         `yaml:"defaultVersionPolicy,omitempty"`
	SchemaObjects        []SchemaObject `yaml:"schemaObjects"`
}

// NodeManifestGovernance describes the temporal validity of a node manifest.
// Unlike NetworkManifestGovernance it carries no Signed field — signature
// verification is handled by the manifest loader infrastructure.
type NodeManifestGovernance struct {
	EffectiveFrom  string `yaml:"effectiveFrom"`
	EffectiveUntil string `yaml:"effectiveUntil"` // optional — omit for indefinite validity
}

// NodeManifest is the typed YAML schema for a node-manifest document.
// It is a sibling to NetworkManifest and shares the same DeDi registry
// placement convention, signing policy, and manifest loader infrastructure.
//
// SubscriberID is the fully-qualified three-part DeDi reference in the format
// namespace/registry/recordId — e.g. "nfh.global/subscribers.beckn.one/bpp.energy-provider.com".
// This corresponds to bapId/bppId in the Beckn transaction context.
type NodeManifest struct {
	ManifestVersion string                 `yaml:"manifestVersion"`
	ManifestType    string                 `yaml:"manifestType"`
	SubscriberID    string                 `yaml:"subscriberId"`
	Schema          NodeManifestSchema     `yaml:"schema"`
	Governance      NodeManifestGovernance `yaml:"governance"`
}

// ParseNodeManifest parses YAML node manifest content.
func ParseNodeManifest(content []byte) (*NodeManifest, error) {
	var manifest NodeManifest
	if err := yaml.Unmarshal(content, &manifest); err != nil {
		return nil, err
	}
	return &manifest, nil
}

// Validate checks the node manifest against schema rules for the given subscriber ID and time.
func (m *NodeManifest) Validate(expectedSubscriberID string, now time.Time) error {
	if m == nil {
		return fmt.Errorf("node manifest for subscriber %q cannot be nil", expectedSubscriberID)
	}
	if strings.TrimSpace(m.ManifestVersion) == "" {
		return fmt.Errorf("node manifest for subscriber %q is missing manifestVersion", expectedSubscriberID)
	}
	if m.ManifestType != NodeManifestType {
		return fmt.Errorf("node manifest for subscriber %q must have manifestType=%q", expectedSubscriberID, NodeManifestType)
	}
	if strings.TrimSpace(m.SubscriberID) == "" {
		return fmt.Errorf("node manifest for subscriber %q is missing subscriberId", expectedSubscriberID)
	}
	parts := strings.Split(m.SubscriberID, "/")
	if len(parts) != 3 || strings.TrimSpace(parts[0]) == "" || strings.TrimSpace(parts[1]) == "" || strings.TrimSpace(parts[2]) == "" {
		return fmt.Errorf("node manifest subscriberId %q must be in namespace/registry/recordId format", m.SubscriberID)
	}
	if m.SubscriberID != expectedSubscriberID {
		return fmt.Errorf("node manifest subscriberId %q does not match expected %q", m.SubscriberID, expectedSubscriberID)
	}
	if m.Schema.DefaultVersionPolicy != "" &&
		m.Schema.DefaultVersionPolicy != VersionPolicyLatest &&
		m.Schema.DefaultVersionPolicy != VersionPolicyPinned {
		return fmt.Errorf("node manifest for subscriber %q has unknown defaultVersionPolicy %q", expectedSubscriberID, m.Schema.DefaultVersionPolicy)
	}
	if len(m.Schema.SchemaObjects) == 0 {
		return fmt.Errorf("node manifest for subscriber %q must have at least one schema object", expectedSubscriberID)
	}
	for i, obj := range m.Schema.SchemaObjects {
		if strings.TrimSpace(obj.Type) == "" {
			return fmt.Errorf("node manifest for subscriber %q: schema object at index %d is missing type", expectedSubscriberID, i)
		}
		if strings.TrimSpace(obj.BaseURL) == "" {
			return fmt.Errorf("node manifest for subscriber %q: schema object at index %d is missing baseUrl", expectedSubscriberID, i)
		}
		if len(obj.SupportedVersions) == 0 {
			return fmt.Errorf("node manifest for subscriber %q: schema object at index %d has no supportedVersions", expectedSubscriberID, i)
		}
		if obj.VersionPolicy != "" && obj.VersionPolicy != VersionPolicyLatest && obj.VersionPolicy != VersionPolicyPinned {
			return fmt.Errorf("node manifest for subscriber %q: schema object at index %d has unknown versionPolicy %q", expectedSubscriberID, i, obj.VersionPolicy)
		}
		if obj.VersionPolicy == VersionPolicyPinned || (obj.VersionPolicy == "" && m.Schema.DefaultVersionPolicy == VersionPolicyPinned) {
			if strings.TrimSpace(obj.PinnedVersion) == "" {
				return fmt.Errorf("node manifest for subscriber %q: schema object at index %d has versionPolicy=pinned but no pinnedVersion", expectedSubscriberID, i)
			}
			found := false
			for _, v := range obj.SupportedVersions {
				if v == obj.PinnedVersion {
					found = true
					break
				}
			}
			if !found {
				return fmt.Errorf("node manifest for subscriber %q: schema object at index %d pinnedVersion %q is not in supportedVersions", expectedSubscriberID, i, obj.PinnedVersion)
			}
		}
	}

	effectiveFrom, err := time.Parse(time.RFC3339, m.Governance.EffectiveFrom)
	if err != nil {
		return fmt.Errorf("node manifest for subscriber %q has invalid governance.effectiveFrom: %w", expectedSubscriberID, err)
	}
	if now.Before(effectiveFrom) {
		return fmt.Errorf("node manifest for subscriber %q is not active until %s", expectedSubscriberID, effectiveFrom.Format(time.RFC3339))
	}

	if m.Governance.EffectiveUntil != "" {
		effectiveUntil, err := time.Parse(time.RFC3339, m.Governance.EffectiveUntil)
		if err != nil {
			return fmt.Errorf("node manifest for subscriber %q has invalid governance.effectiveUntil: %w", expectedSubscriberID, err)
		}
		if !effectiveUntil.After(effectiveFrom) {
			return fmt.Errorf("node manifest for subscriber %q must have governance.effectiveUntil later than governance.effectiveFrom", expectedSubscriberID)
		}
		if now.After(effectiveUntil) {
			return fmt.Errorf("node manifest for subscriber %q expired at %s", expectedSubscriberID, effectiveUntil.Format(time.RFC3339))
		}
	}

	return nil
}
