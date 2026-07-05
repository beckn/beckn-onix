// Baseline generation (network-facilitator side). GenerateBaseline runs the
// same discovery, redaction, and hashing pipeline the verifier runs, against
// the facilitator's reference devkit checkout, and records the results in the
// baseline document that participants later verify against.
package deployconform

import (
	"fmt"
	"sort"
)

// GenerateBaseline fills the computed fields of a baseline spec — per-role
// artifact hashes, canonical content, and root hashes — from the reference
// devkit checkout. The spec must already carry identity (networkId, devkitId,
// releaseId), composePath, and the variance rules; every service declared by
// a role must exist in the compose file, since the facilitator's checkout is
// the definition of conformance.
func GenerateBaseline(devkit *Devkit, spec *Baseline) (*Baseline, error) {
	result := *spec
	result.BaselineVersion = BaselineVersion
	result.BaselineType = BaselineType
	result.HashAlgorithm = HashAlgorithm
	result.Canonicalization = Canonicalization
	result.Placeholder = result.placeholderValue()

	if err := result.Validate("", ""); err != nil {
		return nil, fmt.Errorf("baseline spec: %w", err)
	}

	roleNames := make([]string, 0, len(result.Roles))
	for name := range result.Roles {
		roleNames = append(roleNames, name)
	}
	sort.Strings(roleNames)

	roles := make(map[string]*BaselineRole, len(result.Roles))
	for _, name := range roleNames {
		specRole := result.Roles[name]
		for _, service := range specRole.Services {
			if !devkit.HasService(service) {
				return nil, fmt.Errorf("role %q declares service %q which is not in the compose file", name, service)
			}
		}
		artifacts, err := devkit.RoleArtifacts(specRole.Services, result.Include)
		if err != nil {
			return nil, fmt.Errorf("discover role %q: %w", name, err)
		}
		hashed := make([]BaselineArtifact, 0, len(artifacts))
		for _, a := range artifacts {
			ba, err := hashArtifact(a, result.Variance, result.Placeholder)
			if err != nil {
				return nil, fmt.Errorf("hash artifact %s for role %q: %w", a.ID, name, err)
			}
			hashed = append(hashed, ba)
		}
		roles[name] = &BaselineRole{
			Services:   specRole.Services,
			Artifacts:  hashed,
			RootSHA256: rootHash(hashed),
		}
	}
	result.Roles = roles
	return &result, nil
}

// hashArtifact produces the baseline entry for one discovered artifact:
// variance rules are applied (redacting participant-owned paths, or the whole
// artifact), the result is canonicalized, and both the canonical form and its
// SHA-256 are recorded. The canonical form lets verifiers report the exact
// deviating paths instead of just "hash mismatch".
func hashArtifact(a Artifact, variance []VarianceRule, placeholder string) (BaselineArtifact, error) {
	patterns, wholeArtifact := varianceFor(variance, a.ID)

	var canonical []byte
	var err error
	switch {
	case wholeArtifact:
		// The whole artifact is participant-owned: only its presence is
		// recorded, its content is the placeholder for every participant.
		canonical, err = CanonicalJSON(placeholder)
	case a.Kind == KindRaw:
		canonical = normalizeRaw(a.Raw)
	default:
		canonical, err = CanonicalJSON(redactTree(a.Tree, patterns, placeholder))
	}
	if err != nil {
		return BaselineArtifact{}, err
	}
	return BaselineArtifact{
		ID:        a.ID,
		SHA256:    sha256Hex(canonical),
		Canonical: string(canonical),
	}, nil
}
