package deployconform

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// testSpec returns the baseline spec used by the roundtrip tests: identity
// fields (keys, participant names, ports) are participant-owned, routing
// configs are wholly participant-owned, compose runtime knobs vary.
func testSpec() *Baseline {
	return &Baseline{
		NetworkID:   "example.org/testnet",
		DevkitID:    "mini-devkit",
		ReleaseID:   "2026.07",
		ComposePath: "install/docker-compose.yml",
		Variance: []VarianceRule{
			{
				Artifacts: []string{"config/adapter-*.yaml"},
				Paths: []string{
					"http.port",
					"modules.handler.plugins.keyManager.config",
				},
			},
			{
				Artifacts: []string{"config/routing-*.yaml"},
			},
			{
				Artifacts: []string{"compose:*"},
				Paths:     []string{"ports", "container_name", "environment.REDIS_ADDR"},
			},
		},
		Roles: map[string]*BaselineRole{
			"alpha": {Services: []string{"onix-alpha", "redis-alpha"}},
			"beta":  {Services: []string{"onix-beta"}},
		},
	}
}

// copyDevkit clones the testdata devkit into a temp dir so tests can tamper
// with the copy.
func copyDevkit(t *testing.T) string {
	t.Helper()
	dst := t.TempDir()
	src := filepath.Join("testdata", "devkit")
	err := filepath.Walk(src, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		content, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(target, content, 0o644)
	})
	if err != nil {
		t.Fatalf("copy devkit: %v", err)
	}
	return dst
}

// generateTestBaseline builds a baseline from the pristine testdata devkit.
func generateTestBaseline(t *testing.T) *Baseline {
	t.Helper()
	devkit := testDevkit(t)
	baseline, err := GenerateBaseline(devkit, testSpec())
	if err != nil {
		t.Fatalf("GenerateBaseline: %v", err)
	}
	return baseline
}

// findingByKind returns the first finding of the given kind, if any.
func findingByKind(report *Report, kind FindingKind) (Finding, bool) {
	for _, f := range report.Findings {
		if f.Kind == kind {
			return f, true
		}
	}
	return Finding{}, false
}

// renderDetails joins a finding's details as displayed text, for substring
// assertions.
func renderDetails(f Finding) string {
	lines := make([]string, 0, len(f.Details))
	for _, d := range f.Details {
		lines = append(lines, d.String())
	}
	return strings.Join(lines, "\n")
}

// TestGenerateBaseline checks the generated document's computed fields and
// its YAML roundtrip stability.
func TestGenerateBaseline(t *testing.T) {
	baseline := generateTestBaseline(t)

	if err := baseline.Validate("example.org/testnet", "mini-devkit"); err != nil {
		t.Fatalf("generated baseline invalid: %v", err)
	}
	alpha := baseline.Roles["alpha"]
	if alpha.RootSHA256 == "" || len(alpha.Artifacts) != 7 {
		t.Fatalf("alpha role: root=%q artifacts=%d, want 7 artifacts", alpha.RootSHA256, len(alpha.Artifacts))
	}

	// The document must survive a YAML publish/parse cycle byte-for-byte in
	// hashes, since that is exactly what participants download.
	encoded, err := yaml.Marshal(baseline)
	if err != nil {
		t.Fatalf("marshal baseline: %v", err)
	}
	reparsed, err := ParseBaseline(encoded)
	if err != nil {
		t.Fatalf("reparse baseline: %v", err)
	}
	if reparsed.Roles["alpha"].RootSHA256 != alpha.RootSHA256 {
		t.Fatalf("root hash changed across YAML roundtrip")
	}

	// Generating from a missing service must fail: the facilitator's checkout
	// defines conformance.
	badSpec := testSpec()
	badSpec.Roles["gamma"] = &BaselineRole{Services: []string{"missing-service"}}
	if _, err := GenerateBaseline(testDevkit(t), badSpec); err == nil || !strings.Contains(err.Error(), "missing-service") {
		t.Fatalf("expected missing-service error, got %v", err)
	}
}

// TestVerifyRolePristine checks that an untouched checkout is fully
// compliant, and that participant-owned edits stay compliant.
func TestVerifyRolePristine(t *testing.T) {
	baseline := generateTestBaseline(t)
	devkit := testDevkit(t)

	for _, role := range []string{"alpha", "beta"} {
		report, err := VerifyRole(devkit, baseline, role)
		if err != nil {
			t.Fatalf("VerifyRole(%s): %v", role, err)
		}
		if !report.Compliant() {
			t.Fatalf("pristine devkit not compliant for role %s: %+v", role, report.Findings)
		}
	}

	if _, err := VerifyRole(devkit, baseline, "nope"); err == nil {
		t.Fatalf("expected error for unknown role")
	}
}

// TestVerifyRoleParticipantEdits checks that edits inside declared variance
// (keys, ports, whole routing config) do not raise findings.
func TestVerifyRoleParticipantEdits(t *testing.T) {
	baseline := generateTestBaseline(t)
	root := copyDevkit(t)

	adapter := filepath.Join(root, "config", "adapter-alpha.yaml")
	content, _ := os.ReadFile(adapter)
	edited := strings.ReplaceAll(string(content), "ALPHA-PRIVATE-KEY", "MY-REAL-KEY")
	edited = strings.ReplaceAll(edited, "port: 8081", "port: 9999")
	if err := os.WriteFile(adapter, []byte(edited), 0o644); err != nil {
		t.Fatal(err)
	}
	routing := filepath.Join(root, "config", "routing-alpha.yaml")
	if err := os.WriteFile(routing, []byte("routingRules:\n  - domain: energy\n    targetType: url\n    target:\n      url: https://mine.example.net/receiver\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	devkit, err := LoadDevkit(root, "install/docker-compose.yml")
	if err != nil {
		t.Fatal(err)
	}
	report, err := VerifyRole(devkit, baseline, "alpha")
	if err != nil {
		t.Fatal(err)
	}
	if !report.Compliant() {
		t.Fatalf("participant-owned edits must stay compliant, got %+v", report.Findings)
	}
}

// TestVerifyRoleModified checks that a network-fixed edit (removing a
// pipeline step) is reported with the exact deviating path.
func TestVerifyRoleModified(t *testing.T) {
	baseline := generateTestBaseline(t)
	root := copyDevkit(t)

	adapter := filepath.Join(root, "config", "adapter-alpha.yaml")
	content, _ := os.ReadFile(adapter)
	edited := strings.Replace(string(content), "        - checkPolicy\n", "", 1)
	if edited == string(content) {
		t.Fatal("tamper failed: checkPolicy line not found")
	}
	if err := os.WriteFile(adapter, []byte(edited), 0o644); err != nil {
		t.Fatal(err)
	}

	devkit, err := LoadDevkit(root, "install/docker-compose.yml")
	if err != nil {
		t.Fatal(err)
	}
	report, err := VerifyRole(devkit, baseline, "alpha")
	if err != nil {
		t.Fatal(err)
	}
	if report.Compliant() {
		t.Fatal("removing a pipeline step must be a deviation")
	}
	if report.ExpectedRoot == report.ComputedRoot {
		t.Fatal("root hashes must differ")
	}
	finding, ok := findingByKind(report, FindingModified)
	if !ok || finding.ArtifactID != "config/adapter-alpha.yaml" {
		t.Fatalf("expected modified finding for adapter-alpha.yaml, got %+v", report.Findings)
	}
	details := renderDetails(finding)
	if !strings.Contains(details, "steps") {
		t.Fatalf("finding details should name the steps path, got %v", finding.Details)
	}
}

// TestVerifyRoleImageChanged checks that changing a pinned image tag in the
// compose file is reported at its path.
func TestVerifyRoleImageChanged(t *testing.T) {
	baseline := generateTestBaseline(t)
	root := copyDevkit(t)

	compose := filepath.Join(root, "install", "docker-compose.yml")
	content, _ := os.ReadFile(compose)
	edited := strings.Replace(string(content), "example/onix-adapter:v2", "example/onix-adapter:evil", 1)
	if err := os.WriteFile(compose, []byte(edited), 0o644); err != nil {
		t.Fatal(err)
	}

	devkit, err := LoadDevkit(root, "install/docker-compose.yml")
	if err != nil {
		t.Fatal(err)
	}
	report, err := VerifyRole(devkit, baseline, "alpha")
	if err != nil {
		t.Fatal(err)
	}
	finding, ok := findingByKind(report, FindingModified)
	if !ok || finding.ArtifactID != "compose:onix-alpha" {
		t.Fatalf("expected modified finding for compose:onix-alpha, got %+v", report.Findings)
	}
	details := renderDetails(finding)
	if !strings.Contains(details, "image") || !strings.Contains(details, "evil") {
		t.Fatalf("details should name the image path and new value, got %v", finding.Details)
	}
}

// TestVerifyRoleMissingAndUnexpected checks missing artifacts (deleted rego)
// and unexpected artifacts (new referenced file).
func TestVerifyRoleMissingAndUnexpected(t *testing.T) {
	baseline := generateTestBaseline(t)
	root := copyDevkit(t)

	if err := os.Remove(filepath.Join(root, "policies", "network.rego")); err != nil {
		t.Fatal(err)
	}
	extra := filepath.Join(root, "config", "extra-hook.yaml")
	if err := os.WriteFile(extra, []byte("hook: true\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	adapter := filepath.Join(root, "config", "adapter-alpha.yaml")
	content, _ := os.ReadFile(adapter)
	edited := string(content) + "extraRef: ./config/extra-hook.yaml\n"
	if err := os.WriteFile(adapter, []byte(edited), 0o644); err != nil {
		t.Fatal(err)
	}

	devkit, err := LoadDevkit(root, "install/docker-compose.yml")
	if err != nil {
		t.Fatal(err)
	}
	report, err := VerifyRole(devkit, baseline, "alpha")
	if err != nil {
		t.Fatal(err)
	}

	missing, ok := findingByKind(report, FindingMissing)
	if !ok || missing.ArtifactID != "policies/network.rego" {
		t.Fatalf("expected missing finding for network.rego, got %+v", report.Findings)
	}
	unexpected, ok := findingByKind(report, FindingUnexpected)
	if !ok || unexpected.ArtifactID != "config/extra-hook.yaml" {
		t.Fatalf("expected unexpected finding for extra-hook.yaml, got %+v", report.Findings)
	}
}

// TestVerifyRoleMissingService checks that a declared service absent from
// the local compose file is reported as missing.
func TestVerifyRoleMissingService(t *testing.T) {
	baseline := generateTestBaseline(t)
	root := copyDevkit(t)

	compose := filepath.Join(root, "install", "docker-compose.yml")
	content, _ := os.ReadFile(compose)
	edited := strings.Replace(string(content), "  redis-alpha:\n    image: redis:alpine\n    container_name: redis-alpha\n\n", "", 1)
	if edited == string(content) {
		t.Fatal("tamper failed: redis-alpha block not found")
	}
	if err := os.WriteFile(compose, []byte(edited), 0o644); err != nil {
		t.Fatal(err)
	}

	devkit, err := LoadDevkit(root, "install/docker-compose.yml")
	if err != nil {
		t.Fatal(err)
	}
	report, err := VerifyRole(devkit, baseline, "alpha")
	if err != nil {
		t.Fatal(err)
	}
	missing, ok := findingByKind(report, FindingMissing)
	if !ok || missing.ArtifactID != "compose:redis-alpha" {
		t.Fatalf("expected missing finding for compose:redis-alpha, got %+v", report.Findings)
	}
}

// TestBaselineValidate covers the schema checks of the baseline document.
func TestBaselineValidate(t *testing.T) {
	tests := []struct {
		name       string
		mutate     func(*Baseline)
		wantErrSub string
	}{
		{name: "valid", mutate: func(b *Baseline) {}},
		{
			name:       "wrong type",
			mutate:     func(b *Baseline) { b.BaselineType = "other" },
			wantErrSub: "baselineType",
		},
		{
			name:       "network mismatch",
			mutate:     func(b *Baseline) { b.NetworkID = "other/net" },
			wantErrSub: "does not match manifest network",
		},
		{
			name:       "devkit mismatch",
			mutate:     func(b *Baseline) { b.DevkitID = "other-kit" },
			wantErrSub: "does not match manifest deployment.devkitId",
		},
		{
			name:       "unsupported hash",
			mutate:     func(b *Baseline) { b.HashAlgorithm = "md5" },
			wantErrSub: "unsupported hashAlgorithm",
		},
		{
			name:       "unsupported canonicalization",
			mutate:     func(b *Baseline) { b.Canonicalization = "jcs/2" },
			wantErrSub: "unsupported canonicalization",
		},
		{
			name:       "no roles",
			mutate:     func(b *Baseline) { b.Roles = nil },
			wantErrSub: "at least one role",
		},
		{
			name:       "role without services",
			mutate:     func(b *Baseline) { b.Roles["alpha"].Services = nil },
			wantErrSub: `role "alpha" must list at least one`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			baseline := generateTestBaseline(t)
			tt.mutate(baseline)
			err := baseline.Validate("example.org/testnet", "mini-devkit")
			if tt.wantErrSub == "" {
				if err != nil {
					t.Fatalf("expected success, got %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErrSub) {
				t.Fatalf("expected error containing %q, got %v", tt.wantErrSub, err)
			}
		})
	}
}
