// Baseline generation and verification — the two symmetric halves of the
// conformance comparison.
//
// GenerateBaseline (network-facilitator side) runs the discovery, redaction,
// and hashing pipeline against the facilitator's reference devkit checkout
// and records the results in the baseline document. VerifyRole (participant
// side) reruns the identical pipeline over the local checkout and compares:
// a matching root hash means full conformance; on mismatch, each modified
// artifact is diffed against its baseline canonical form so the report names
// the exact deviating paths.
package deployconform

import (
	"encoding/json"
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

const (
	// maxDiffDetails caps the number of deviating paths reported per artifact.
	maxDiffDetails = 50
	// maxDiffValueLen caps how much of a deviating value appears in a report.
	maxDiffValueLen = 80
)

// VerifyRole verifies the local devkit checkout against one role of the
// baseline, discovering the role's artifacts itself. See VerifyRoleArtifacts
// for the variant that reuses an existing discovery result.
func VerifyRole(devkit *Devkit, baseline *Baseline, roleName string) (*Report, error) {
	role, ok := baseline.Roles[roleName]
	if !ok {
		return nil, fmt.Errorf("baseline does not define role %q", roleName)
	}
	artifacts, err := devkit.RoleArtifacts(role.Services, baseline.Include)
	if err != nil {
		return nil, fmt.Errorf("discover local artifacts: %w", err)
	}
	return VerifyRoleArtifacts(baseline, roleName, artifacts)
}

// VerifyRoleArtifacts verifies one baseline role against an already
// discovered artifact set, so a caller that also needs the artifacts (e.g.
// for policy input) discovers exactly once and both layers see the same
// snapshot. Deviations are returned in the report, never as an error; the
// error path is reserved for the verification process itself failing.
func VerifyRoleArtifacts(baseline *Baseline, roleName string, artifacts []Artifact) (*Report, error) {
	role, ok := baseline.Roles[roleName]
	if !ok {
		return nil, fmt.Errorf("baseline does not define role %q", roleName)
	}

	placeholder := baseline.placeholderValue()
	local := make(map[string]BaselineArtifact, len(artifacts))
	localList := make([]BaselineArtifact, 0, len(artifacts))
	for _, a := range artifacts {
		ba, err := hashArtifact(a, baseline.Variance, placeholder)
		if err != nil {
			return nil, fmt.Errorf("hash local artifact %s: %w", a.ID, err)
		}
		local[ba.ID] = ba
		localList = append(localList, ba)
	}

	report := &Report{
		NetworkID:    baseline.NetworkID,
		DevkitID:     baseline.DevkitID,
		ReleaseID:    baseline.ReleaseID,
		Role:         roleName,
		ExpectedRoot: role.RootSHA256,
		ComputedRoot: rootHash(localList),
	}

	for _, want := range role.Artifacts {
		got, exists := local[want.ID]
		if !exists {
			report.Findings = append(report.Findings, Finding{ArtifactID: want.ID, Kind: FindingMissing})
			continue
		}
		if got.SHA256 != want.SHA256 {
			report.Findings = append(report.Findings, Finding{
				ArtifactID: want.ID,
				Kind:       FindingModified,
				Details:    diffCanonical(want.Canonical, got.Canonical),
			})
		}
	}

	known := make(map[string]bool, len(role.Artifacts))
	for _, want := range role.Artifacts {
		known[want.ID] = true
	}
	extras := make([]string, 0)
	for id := range local {
		if !known[id] {
			extras = append(extras, id)
		}
	}
	sort.Strings(extras)
	for _, id := range extras {
		report.Findings = append(report.Findings, Finding{ArtifactID: id, Kind: FindingUnexpected})
	}
	return report, nil
}

// diffCanonical compares two canonical artifact encodings and describes where
// they differ. Structured artifacts are diffed path by path; anything that
// does not parse as JSON (raw artifacts such as Rego files) gets a single
// summary line — its full content diff belongs in a text tool, not a report.
func diffCanonical(want, got string) []FindingDetail {
	var wantTree, gotTree any
	if json.Unmarshal([]byte(want), &wantTree) != nil || json.Unmarshal([]byte(got), &gotTree) != nil {
		return []FindingDetail{{Message: "content differs from the network baseline"}}
	}
	var details []FindingDetail
	diffTrees(wantTree, gotTree, "", &details)
	if len(details) == 0 {
		// Hashes differed but the parsed trees do not — canonicalization
		// drift between versions of this tool. Surface it rather than hide it.
		details = []FindingDetail{{Message: "canonical encoding differs (tool version mismatch?)"}}
	}
	return details
}

// diffTrees walks two parsed trees in lockstep, appending one detail per
// deviating path until maxDiffDetails is reached. The baseline side is
// "expected"; the local deployment is "got". Values are truncated so a
// mis-pasted blob cannot flood the report.
func diffTrees(want, got any, path string, out *[]FindingDetail) {
	if len(*out) >= maxDiffDetails {
		return
	}
	switch w := want.(type) {
	case map[string]any:
		g, ok := got.(map[string]any)
		if !ok {
			appendDiff(out, path, want, got)
			return
		}
		keys := make([]string, 0, len(w)+len(g))
		for k := range w {
			keys = append(keys, k)
		}
		for k := range g {
			if _, dup := w[k]; !dup {
				keys = append(keys, k)
			}
		}
		sort.Strings(keys)
		for _, k := range keys {
			wv, inWant := w[k]
			gv, inGot := g[k]
			switch {
			case !inGot:
				*out = append(*out, FindingDetail{Path: joinPath(path, k), Message: "required by the network baseline but missing"})
			case !inWant:
				*out = append(*out, FindingDetail{Path: joinPath(path, k), Message: "not part of the network baseline"})
			default:
				diffTrees(wv, gv, joinPath(path, k), out)
			}
			if len(*out) >= maxDiffDetails {
				return
			}
		}
	case []any:
		g, ok := got.([]any)
		if !ok {
			appendDiff(out, path, want, got)
			return
		}
		if len(w) != len(g) {
			*out = append(*out, FindingDetail{Path: path, Message: fmt.Sprintf("list has %d entries, baseline expects %d", len(g), len(w))})
			return
		}
		for i := range w {
			diffTrees(w[i], g[i], fmt.Sprintf("%s[%d]", path, i), out)
			if len(*out) >= maxDiffDetails {
				return
			}
		}
	default:
		if want != got {
			appendDiff(out, path, want, got)
		}
	}
}

// appendDiff records one scalar deviation as "expected X, got Y" at path.
func appendDiff(out *[]FindingDetail, path string, want, got any) {
	*out = append(*out, FindingDetail{
		Path:    path,
		Message: fmt.Sprintf("expected %s, got %s", truncateValue(want), truncateValue(got)),
	})
}

// joinPath extends a dot-notation path with one map key.
func joinPath(path, key string) string {
	if path == "" {
		return key
	}
	return path + "." + key
}

// truncateValue renders a value for a report line, bounded in length.
func truncateValue(v any) string {
	enc, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprintf("%T", v)
	}
	s := string(enc)
	if len(s) > maxDiffValueLen {
		s = s[:maxDiffValueLen] + "…"
	}
	return s
}
