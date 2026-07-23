// Package deployconform implements network deployment conformance: a network
// facilitator publishes a signed baseline describing the ideal devkit
// configuration (docker-compose file plus every local file it references,
// canonicalized and hashed with participant-specific values redacted), and
// participants verify their deployed configuration against that baseline.
// Deviations are reported as warnings and, optionally, emitted as telemetry
// events to the network's observability collector.
//
// The baseline document is distributed through the network manifest's
// `deployment` section (see pkg/model.NetworkManifestDeployment) with the same
// detached-signature verification used for network policies.
package deployconform

import (
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

const (
	// BaselineType is the required baselineType value of a baseline document.
	BaselineType = "deployment-baseline"
	// BaselineVersion is the baseline schema version written by this package.
	BaselineVersion = "1.0"
	// Canonicalization names the artifact canonicalization scheme used for
	// hashing: compact JSON with lexicographically sorted object keys.
	Canonicalization = "canonical-json/1"
	// HashAlgorithm is the only supported artifact hash algorithm.
	HashAlgorithm = "sha256"
	// DefaultPlaceholder replaces participant-specific values before hashing,
	// so every compliant participant produces identical artifact hashes.
	DefaultPlaceholder = "__PARTICIPANT_SPECIFIC__"

	// composeArtifactPrefix prefixes per-service compose artifact IDs, e.g.
	// "compose:onix-buyerapp". File artifacts use root-relative slash paths.
	composeArtifactPrefix = "compose:"
)

// VarianceRule declares which parts of which artifacts are participant-owned.
// Artifacts lists artifact-ID globs (path.Match syntax). Paths lists dot
// notation patterns into YAML/JSON artifacts ("*" matches any key, lists are
// traversed transparently); each matched value is replaced by the placeholder
// before hashing. An empty Paths list marks the whole artifact as
// participant-owned: its content is not hashed at all.
type VarianceRule struct {
	Artifacts []string `yaml:"artifacts"`
	Paths     []string `yaml:"paths"`
}

// BaselineArtifact is one hashed artifact in a role's baseline: a compose
// service subtree ("compose:<service>") or a devkit file (root-relative
// path). Canonical carries the redacted canonical form used for the hash so
// verifiers can pinpoint the deviating paths, not just the deviating file.
type BaselineArtifact struct {
	ID        string `yaml:"id"`
	SHA256    string `yaml:"sha256"`
	Canonical string `yaml:"canonical,omitempty"`
}

// BaselineRole is the slice of the devkit one network participant deploys:
// the compose services it runs and the artifacts discovered from them.
// RootSHA256 commits to the full artifact list so compliance is a single
// hash comparison.
type BaselineRole struct {
	Services   []string           `yaml:"services"`
	Artifacts  []BaselineArtifact `yaml:"artifacts,omitempty"`
	RootSHA256 string             `yaml:"rootSha256,omitempty"`
}

// Baseline is the deployment-baseline document published by the network
// facilitator. The same type doubles as the generation input ("spec"): the
// facilitator authors everything except per-role Artifacts and RootSHA256,
// which GenerateBaseline computes from the reference devkit checkout.
type Baseline struct {
	BaselineVersion  string                   `yaml:"baselineVersion"`
	BaselineType     string                   `yaml:"baselineType"`
	NetworkID        string                   `yaml:"networkId"`
	DevkitID         string                   `yaml:"devkitId"`
	ReleaseID        any                      `yaml:"releaseId"`
	HashAlgorithm    string                   `yaml:"hashAlgorithm"`
	Canonicalization string                   `yaml:"canonicalization"`
	Placeholder      string                   `yaml:"placeholder"`
	ComposePath      string                   `yaml:"composePath"`
	Include          []string                 `yaml:"include,omitempty"`
	Variance         []VarianceRule           `yaml:"variance,omitempty"`
	Roles            map[string]*BaselineRole `yaml:"roles"`
}

// ParseBaseline parses a YAML baseline document without validating it; call
// Validate before trusting the result.
func ParseBaseline(content []byte) (*Baseline, error) {
	var b Baseline
	if err := yaml.Unmarshal(content, &b); err != nil {
		return nil, fmt.Errorf("parse baseline: %w", err)
	}
	return &b, nil
}

// Validate checks the baseline's schema invariants. expectedNetworkID and
// expectedDevkitID bind the document to the manifest that referenced it; pass
// "" to skip either binding (generation-time validation of a spec).
func (b *Baseline) Validate(expectedNetworkID, expectedDevkitID string) error {
	if b == nil {
		return fmt.Errorf("baseline cannot be nil")
	}
	if b.BaselineType != BaselineType {
		return fmt.Errorf("baseline must have baselineType=%q", BaselineType)
	}
	if strings.TrimSpace(b.BaselineVersion) == "" {
		return fmt.Errorf("baseline is missing baselineVersion")
	}
	if strings.TrimSpace(b.NetworkID) == "" {
		return fmt.Errorf("baseline is missing networkId")
	}
	if expectedNetworkID != "" && b.NetworkID != expectedNetworkID {
		return fmt.Errorf("baseline networkId %q does not match manifest network %q", b.NetworkID, expectedNetworkID)
	}
	if strings.TrimSpace(b.DevkitID) == "" {
		return fmt.Errorf("baseline is missing devkitId")
	}
	if expectedDevkitID != "" && b.DevkitID != expectedDevkitID {
		return fmt.Errorf("baseline devkitId %q does not match manifest deployment.devkitId %q", b.DevkitID, expectedDevkitID)
	}
	if b.HashAlgorithm != "" && b.HashAlgorithm != HashAlgorithm {
		return fmt.Errorf("baseline uses unsupported hashAlgorithm %q (only %q is supported)", b.HashAlgorithm, HashAlgorithm)
	}
	if b.Canonicalization != "" && b.Canonicalization != Canonicalization {
		return fmt.Errorf("baseline uses unsupported canonicalization %q (only %q is supported)", b.Canonicalization, Canonicalization)
	}
	if strings.TrimSpace(b.ComposePath) == "" {
		return fmt.Errorf("baseline is missing composePath")
	}
	if len(b.Roles) == 0 {
		return fmt.Errorf("baseline must declare at least one role")
	}
	for name, role := range b.Roles {
		if role == nil || len(role.Services) == 0 {
			return fmt.Errorf("baseline role %q must list at least one compose service", name)
		}
	}
	return nil
}

// placeholderValue returns the configured redaction placeholder, defaulting
// to DefaultPlaceholder when the document omits it.
func (b *Baseline) placeholderValue() string {
	if strings.TrimSpace(b.Placeholder) == "" {
		return DefaultPlaceholder
	}
	return b.Placeholder
}

// FindingKind classifies a single conformance deviation.
type FindingKind string

const (
	// FindingModified marks an artifact whose redacted content differs from
	// the baseline.
	FindingModified FindingKind = "modified"
	// FindingMissing marks a baseline artifact absent from the deployment.
	FindingMissing FindingKind = "missing"
	// FindingUnexpected marks a locally discovered artifact the baseline does
	// not know about.
	FindingUnexpected FindingKind = "unexpected"
	// FindingPolicy marks a deployment-policy (Rego) violation.
	FindingPolicy FindingKind = "policy"
)

// FindingDetail is one structured deviation detail. Path names the deviating
// location inside the artifact (dot notation; empty for detail lines that are
// not tied to a path, such as policy violation messages). Message describes
// the deviation and may contain local configuration values — renderers for
// local output show it, the telemetry channel drops it for modified
// artifacts so local values never leave the host.
type FindingDetail struct {
	Path    string `json:"path,omitempty"`
	Message string `json:"message,omitempty"`
}

// String renders the detail for local, human-readable output.
func (d FindingDetail) String() string {
	if d.Path == "" {
		return d.Message
	}
	if d.Message == "" {
		return d.Path
	}
	return d.Path + ": " + d.Message
}

// Finding is one deviation between the deployed configuration and the
// network baseline. Details lists the deviating paths inside the artifact
// (for FindingModified) or the policy violation messages (for FindingPolicy).
type Finding struct {
	ArtifactID string          `json:"artifactId,omitempty"`
	Kind       FindingKind     `json:"kind"`
	Details    []FindingDetail `json:"details,omitempty"`
}

// Rename records that a baseline file artifact is present locally under a
// different name with conformant content. Renames are transparency, not
// deviations: file identity is content, the name is a label.
type Rename struct {
	From string `json:"from"`
	To   string `json:"to"`
}

// Report is the outcome of verifying one role of a deployment against the
// network baseline.
type Report struct {
	NetworkID      string    `json:"networkId"`
	DevkitID       string    `json:"devkitId"`
	ReleaseID      any       `json:"releaseId,omitempty"`
	Role           string    `json:"role"`
	ExpectedRoot   string    `json:"expectedRoot"`
	ComputedRoot   string    `json:"computedRoot"`
	Findings       []Finding `json:"findings,omitempty"`
	Renames        []Rename  `json:"renames,omitempty"`
	BaselineDigest string    `json:"baselineDigest,omitempty"`
}

// Compliant reports whether the role matches the baseline exactly: root
// hashes agree and no findings were recorded.
func (r *Report) Compliant() bool {
	return len(r.Findings) == 0 && r.ExpectedRoot == r.ComputedRoot
}
