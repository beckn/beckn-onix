package opapolicychecker

import (
	"bytes"
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/open-policy-agent/opa/v1/bundle"

	"github.com/beckn-one/beckn-onix/pkg/model"
	"github.com/beckn-one/beckn-onix/pkg/security/artifactverifier"
)

type stubManifestLoader struct {
	docs map[string]*model.ManifestDocument
	err  error
}

func (s stubManifestLoader) GetByNetworkID(ctx context.Context, networkID string) (*model.ManifestDocument, error) {
	if s.err != nil {
		return nil, s.err
	}
	doc, ok := s.docs[networkID]
	if !ok {
		return nil, fmt.Errorf("manifest not found for %s", networkID)
	}
	return doc, nil
}

func (s stubManifestLoader) GetByMetadata(ctx context.Context, metadata model.ManifestMetadata) (*model.ManifestDocument, error) {
	return nil, errors.New("not implemented in test")
}

// Helper: create a StepContext with the given action path and JSON body.
func makeStepCtx(action string, body string) *model.StepContext {
	req, _ := http.NewRequest("POST", "/bpp/caller/"+action, nil)
	return &model.StepContext{
		Context: context.Background(),
		Request: req,
		Body:    []byte(body),
	}
}

// Helper: write a .rego file to a temp dir and return the dir path.
func writePolicyDir(t *testing.T, filename, content string) string {
	t.Helper()
	dir := t.TempDir()
	err := os.WriteFile(filepath.Join(dir, filename), []byte(content), 0644)
	if err != nil {
		t.Fatalf("failed to write policy file: %v", err)
	}
	return dir
}

func writeNetworkPolicyConfig(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "network-policies.yaml")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("failed to write network policy config: %v", err)
	}
	return path
}

func writeDefaultOnlyNetworkPolicyConfig(t *testing.T, entry string) string {
	t.Helper()
	return writeNetworkPolicyConfig(t, "networkPolicies:\n  default:\n"+indentYAML(entry, "    "))
}

func validManifestGovernanceYAML() string {
	return fmt.Sprintf("governance:\n  effective_from: %q\n  effective_until: %q\n  signed: true\n",
		time.Now().UTC().Add(-1*time.Hour).Format(time.RFC3339),
		time.Now().UTC().Add(1*time.Hour).Format(time.RFC3339),
	)
}

func validNetworkManifestForTest(networkID string, now time.Time) networkManifest {
	signed := true
	return networkManifest{
		ManifestVersion: "1.0",
		ManifestType:    "network-manifest",
		NetworkID:       networkID,
		ReleaseID:       "2026.02",
		Publisher: networkManifestPublisher{
			Role:   "NFO",
			Domain: "nfh.global",
		},
		Policies: &networkManifestPolicies{
			Type:   "rego",
			Source: "file",
			File: &networkManifestFile{
				ID:                        "network-policy-file",
				URL:                       "https://example.com/policy.rego",
				PolicyQueryPath:           "data.logistics.result",
				Signed:                    true,
				SignatureURL:              "https://example.com/policy.rego.sig",
				SigningPublicKeyLookupURL: "https://example.com/public-key",
			},
		},
		Governance: networkManifestGovernance{
			EffectiveFrom:  now.Add(-1 * time.Hour).Format(time.RFC3339),
			EffectiveUntil: now.Add(1 * time.Hour).Format(time.RFC3339),
			Signed:         &signed,
		},
	}
}

func indentYAML(s, prefix string) string {
	lines := strings.Split(strings.TrimSuffix(s, "\n"), "\n")
	for i, line := range lines {
		lines[i] = prefix + line
	}
	return strings.Join(lines, "\n") + "\n"
}

// --- Config Tests ---

func TestParseConfig_RequiresNetworkPolicyConfig(t *testing.T) {
	_, err := ParseConfig(map[string]string{})
	if err == nil {
		t.Fatal("expected error when networkPolicyConfig is missing")
	}
}

func TestParseConfig_Defaults(t *testing.T) {
	cfg, err := ParseConfig(map[string]string{
		"networkPolicyConfig": "/tmp/network-policies.yaml",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !cfg.Enabled {
		t.Error("expected enabled=true by default")
	}
}

func TestParsePolicyConfig_RequiresQuery(t *testing.T) {
	_, err := parsePolicyConfig(map[string]string{
		"type":     "dir",
		"location": "/tmp",
	})
	if err == nil {
		t.Fatal("expected error when no query given")
	}
}

func TestParseConfig_NetworkPolicyConfig(t *testing.T) {
	cfg, err := ParseConfig(map[string]string{
		"networkPolicyConfig": "/tmp/network-policies.yaml",
		"refreshInterval":     "5m",
		"sharedKey":           "sharedValue",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.NetworkPolicyConfig != "/tmp/network-policies.yaml" {
		t.Fatalf("expected networkPolicyConfig to be set, got %q", cfg.NetworkPolicyConfig)
	}
	if cfg.RefreshInterval != 5*time.Minute {
		t.Fatalf("expected refresh interval 5m, got %v", cfg.RefreshInterval)
	}
	if cfg.RuntimeConfig["sharedKey"] != "sharedValue" {
		t.Fatalf("expected shared runtime config to be preserved, got %q", cfg.RuntimeConfig["sharedKey"])
	}
}

func TestParsePolicyConfig_RuntimeConfigForwarding(t *testing.T) {
	cfg, err := parsePolicyConfig(map[string]string{
		"type":                 "dir",
		"location":             "/tmp",
		"query":                "data.policy.violations",
		"minDeliveryLeadHours": "6",
		"customParam":          "value",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.RuntimeConfig["minDeliveryLeadHours"] != "6" {
		t.Errorf("expected minDeliveryLeadHours=6, got %q", cfg.RuntimeConfig["minDeliveryLeadHours"])
	}
	if cfg.RuntimeConfig["customParam"] != "value" {
		t.Errorf("expected customParam=value, got %q", cfg.RuntimeConfig["customParam"])
	}
}

func TestParsePolicyConfig_CustomActions(t *testing.T) {
	cfg, err := parsePolicyConfig(map[string]string{
		"type":     "dir",
		"location": "/tmp",
		"query":    "data.policy.violations",
		"actions":  "confirm, select, init",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cfg.Actions) != 3 {
		t.Fatalf("expected 3 actions, got %d: %v", len(cfg.Actions), cfg.Actions)
	}
	expected := []string{"confirm", "select", "init"}
	for i, want := range expected {
		if cfg.Actions[i] != want {
			t.Errorf("action[%d] = %q, want %q", i, cfg.Actions[i], want)
		}
	}
}

func TestParsePolicyConfig_PolicyPaths(t *testing.T) {
	cfg, err := parsePolicyConfig(map[string]string{
		"type":     "file",
		"location": "https://example.com/a.rego",
		"query":    "data.policy.violations",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cfg.PolicyPaths) != 1 {
		t.Fatalf("expected 1 path, got %d: %v", len(cfg.PolicyPaths), cfg.PolicyPaths)
	}
	if cfg.PolicyPaths[0] != "https://example.com/a.rego" {
		t.Errorf("path[0] = %q", cfg.PolicyPaths[0])
	}
}

func TestParsePolicyConfig_ManifestType(t *testing.T) {
	cfg, err := parsePolicyConfig(map[string]string{
		"type":    "manifest",
		"actions": "confirm",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Type != "manifest" {
		t.Fatalf("expected type manifest, got %q", cfg.Type)
	}
	if cfg.Location != "" || cfg.Query != "" || len(cfg.PolicyPaths) != 0 {
		t.Fatalf("expected manifest config to defer location/query resolution, got %#v", cfg)
	}
}

func TestValidateNetworkManifest(t *testing.T) {
	now := time.Date(2026, time.April, 22, 12, 0, 0, 0, time.UTC)
	expectedNetworkID := "nfh.global/testnet"

	tests := []struct {
		name       string
		mutate     func(*networkManifest)
		wantErrSub string
	}{
		{
			name: "valid",
		},
		{
			name: "invalid manifest type",
			mutate: func(manifest *networkManifest) {
				manifest.ManifestType = "other"
			},
			wantErrSub: `must have manifest_type="network-manifest"`,
		},
		{
			name: "missing policies section",
			mutate: func(manifest *networkManifest) {
				manifest.Policies = nil
			},
			wantErrSub: "missing policies section",
		},
		{
			name: "network mismatch",
			mutate: func(manifest *networkManifest) {
				manifest.NetworkID = "example/logistics"
			},
			wantErrSub: `does not match configured network "nfh.global/testnet"`,
		},
		{
			name: "invalid effective until",
			mutate: func(manifest *networkManifest) {
				manifest.Governance.EffectiveUntil = "not-a-timestamp"
			},
			wantErrSub: "invalid governance.effective_until",
		},
		{
			name: "expired manifest",
			mutate: func(manifest *networkManifest) {
				manifest.Governance.EffectiveUntil = now.Add(-1 * time.Minute).Format(time.RFC3339)
			},
			wantErrSub: "expired at",
		},
		{
			name: "unsupported source",
			mutate: func(manifest *networkManifest) {
				manifest.Policies.Source = "archive"
				manifest.Policies.File = nil
			},
			wantErrSub: `uses unsupported policies.source "archive"`,
		},
		{
			name: "signed file missing signature url",
			mutate: func(manifest *networkManifest) {
				manifest.Policies.File.SignatureURL = ""
			},
			wantErrSub: "requires policies.file.signature_url",
		},
		{
			name: "signed bundle missing public key lookup",
			mutate: func(manifest *networkManifest) {
				manifest.Policies.Source = "bundle"
				manifest.Policies.File = nil
				manifest.Policies.Bundle = &networkManifestBundle{
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

			err := validateNetworkManifest(&manifest, expectedNetworkID, now)
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

// --- Evaluator Tests (with inline policies) ---

func TestEvaluator_NoViolations(t *testing.T) {
	policy := `
package policy
import rego.v1
violations contains msg if {
    input.value < 0
    msg := "value is negative"
}
`
	dir := writePolicyDir(t, "test.rego", policy)
	eval, err := NewEvaluator([]string{dir}, "data.policy.violations", nil, false, 0, nil)
	if err != nil {
		t.Fatalf("NewEvaluator failed: %v", err)
	}

	violations, err := eval.Evaluate(context.Background(), []byte(`{"value": 10}`))
	if err != nil {
		t.Fatalf("Evaluate failed: %v", err)
	}
	if len(violations) != 0 {
		t.Errorf("expected 0 violations, got %d: %v", len(violations), violations)
	}
}

func TestEvaluator_WithViolation(t *testing.T) {
	policy := `
package policy
import rego.v1
violations contains msg if {
    input.value < 0
    msg := "value is negative"
}
`
	dir := writePolicyDir(t, "test.rego", policy)
	eval, err := NewEvaluator([]string{dir}, "data.policy.violations", nil, false, 0, nil)
	if err != nil {
		t.Fatalf("NewEvaluator failed: %v", err)
	}

	violations, err := eval.Evaluate(context.Background(), []byte(`{"value": -5}`))
	if err != nil {
		t.Fatalf("Evaluate failed: %v", err)
	}
	if len(violations) != 1 {
		t.Fatalf("expected 1 violation, got %d: %v", len(violations), violations)
	}
	if violations[0] != "value is negative" {
		t.Errorf("unexpected violation: %q", violations[0])
	}
}

func TestEvaluator_RuntimeConfig(t *testing.T) {
	policy := `
package policy
import rego.v1
violations contains msg if {
    input.value > to_number(data.config.maxValue)
    msg := "value exceeds maximum"
}
`
	dir := writePolicyDir(t, "test.rego", policy)
	eval, err := NewEvaluator([]string{dir}, "data.policy.violations", map[string]string{"maxValue": "100"}, false, 0, nil)
	if err != nil {
		t.Fatalf("NewEvaluator failed: %v", err)
	}

	// Under limit
	violations, err := eval.Evaluate(context.Background(), []byte(`{"value": 50}`))
	if err != nil {
		t.Fatalf("Evaluate failed: %v", err)
	}
	if len(violations) != 0 {
		t.Errorf("expected 0 violations for value=50, got %v", violations)
	}

	// Over limit
	violations, err = eval.Evaluate(context.Background(), []byte(`{"value": 150}`))
	if err != nil {
		t.Fatalf("Evaluate failed: %v", err)
	}
	if len(violations) != 1 {
		t.Errorf("expected 1 violation for value=150, got %v", violations)
	}
}

func TestEvaluator_SkipsTestFiles(t *testing.T) {
	dir := t.TempDir()

	policy := `
package policy
import rego.v1
violations contains "always" if { true }
`
	os.WriteFile(filepath.Join(dir, "policy.rego"), []byte(policy), 0644)

	// Test file would cause compilation issues if loaded (different package)
	testFile := `
package policy_test
import rego.v1
import data.policy
test_something if { count(policy.violations) > 0 }
`
	os.WriteFile(filepath.Join(dir, "policy_test.rego"), []byte(testFile), 0644)

	eval, err := NewEvaluator([]string{dir}, "data.policy.violations", nil, false, 0, nil)
	if err != nil {
		t.Fatalf("NewEvaluator should skip _test.rego files, but failed: %v", err)
	}

	violations, err := eval.Evaluate(context.Background(), []byte(`{}`))
	if err != nil {
		t.Fatalf("Evaluate failed: %v", err)
	}
	if len(violations) != 1 {
		t.Errorf("expected 1 violation, got %d", len(violations))
	}
}

func TestEvaluator_InvalidJSON(t *testing.T) {
	policy := `
package policy
import rego.v1
violations := set()
`
	dir := writePolicyDir(t, "test.rego", policy)
	eval, err := NewEvaluator([]string{dir}, "data.policy.violations", nil, false, 0, nil)
	if err != nil {
		t.Fatalf("NewEvaluator failed: %v", err)
	}

	_, err = eval.Evaluate(context.Background(), []byte(`not json`))
	if err == nil {
		t.Error("expected error for invalid JSON")
	}
}

// --- Evaluator URL Fetch Tests ---

func TestEvaluator_FetchFromURL(t *testing.T) {
	policy := `
package policy
import rego.v1
violations contains msg if {
    input.value < 0
    msg := "value is negative"
}
`
	// Serve the policy via a local HTTP server
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.Write([]byte(policy))
	}))
	defer srv.Close()

	eval, err := NewEvaluator([]string{srv.URL + "/test_policy.rego"}, "data.policy.violations", nil, false, 0, nil)
	if err != nil {
		t.Fatalf("NewEvaluator with URL failed: %v", err)
	}

	// Compliant
	violations, err := eval.Evaluate(context.Background(), []byte(`{"value": 10}`))
	if err != nil {
		t.Fatalf("Evaluate failed: %v", err)
	}
	if len(violations) != 0 {
		t.Errorf("expected 0 violations, got %v", violations)
	}

	// Non-compliant
	violations, err = eval.Evaluate(context.Background(), []byte(`{"value": -1}`))
	if err != nil {
		t.Fatalf("Evaluate failed: %v", err)
	}
	if len(violations) != 1 {
		t.Errorf("expected 1 violation, got %v", violations)
	}
}

func TestEvaluator_FetchURL_NotFound(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	defer srv.Close()

	_, err := NewEvaluator([]string{srv.URL + "/missing.rego"}, "data.policy.violations", nil, false, 0, nil)
	if err == nil {
		t.Fatal("expected error for 404 URL")
	}
}

func TestEvaluator_FetchURL_InvalidScheme(t *testing.T) {
	_, err := NewEvaluator([]string{"ftp://example.com/policy.rego"}, "data.policy.violations", nil, false, 0, nil)
	if err == nil {
		t.Fatal("expected error for ftp:// scheme")
	}
}

func TestEvaluator_MixedLocalAndURL(t *testing.T) {
	// Local policy
	localPolicy := `
package policy
import rego.v1
violations contains "local_violation" if { input.local_bad }
`
	dir := writePolicyDir(t, "local.rego", localPolicy)

	// Remote policy (different rule, same package)
	remotePolicy := `
package policy
import rego.v1
violations contains "remote_violation" if { input.remote_bad }
`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(remotePolicy))
	}))
	defer srv.Close()

	eval, err := NewEvaluator([]string{dir, srv.URL + "/remote.rego"}, "data.policy.violations", nil, false, 0, nil)
	if err != nil {
		t.Fatalf("NewEvaluator failed: %v", err)
	}

	// Trigger both violations
	violations, err := eval.Evaluate(context.Background(), []byte(`{"local_bad": true, "remote_bad": true}`))
	if err != nil {
		t.Fatalf("Evaluate failed: %v", err)
	}
	if len(violations) != 2 {
		t.Errorf("expected 2 violations (local+remote), got %d: %v", len(violations), violations)
	}
}

// --- Evaluator with local file path in policySources ---

func TestEvaluator_LocalFilePath(t *testing.T) {
	policy := `
package policy
import rego.v1
violations contains "from_file" if { input.bad }
`
	dir := t.TempDir()
	policyPath := filepath.Join(dir, "local_policy.rego")
	os.WriteFile(policyPath, []byte(policy), 0644)

	eval, err := NewEvaluator([]string{policyPath}, "data.policy.violations", nil, false, 0, nil)
	if err != nil {
		t.Fatalf("NewEvaluator with local path failed: %v", err)
	}

	violations, err := eval.Evaluate(context.Background(), []byte(`{"bad": true}`))
	if err != nil {
		t.Fatalf("Evaluate failed: %v", err)
	}
	if len(violations) != 1 || violations[0] != "from_file" {
		t.Errorf("expected [from_file], got %v", violations)
	}
}

// --- Rego Modularity Tests ---
// These tests prove that rego files can reference each other, supporting
// modular policy design to avoid rule bloat.

// TestEvaluator_CrossFileModularity verifies that multiple .rego files
// in the SAME package automatically share rules and data.
func TestEvaluator_CrossFileModularity(t *testing.T) {
	dir := t.TempDir()

	// File 1: defines a helper rule
	helpers := `
package policy
import rego.v1
is_high_value if { input.message.order.value > 10000 }
`
	os.WriteFile(filepath.Join(dir, "helpers.rego"), []byte(helpers), 0644)

	// File 2: uses the helper from file 1 (same package, auto-merged)
	rules := `
package policy
import rego.v1
violations contains "order too large" if { is_high_value }
`
	os.WriteFile(filepath.Join(dir, "rules.rego"), []byte(rules), 0644)

	eval, err := NewEvaluator([]string{dir}, "data.policy.violations", nil, false, 0, nil)
	if err != nil {
		t.Fatalf("NewEvaluator failed: %v", err)
	}

	// High value order — should trigger
	violations, err := eval.Evaluate(context.Background(), []byte(`{"message":{"order":{"value":15000}}}`))
	if err != nil {
		t.Fatalf("Evaluate failed: %v", err)
	}
	if len(violations) != 1 || violations[0] != "order too large" {
		t.Errorf("expected [order too large], got %v", violations)
	}

	// Low value order — should not trigger
	violations, err = eval.Evaluate(context.Background(), []byte(`{"message":{"order":{"value":500}}}`))
	if err != nil {
		t.Fatalf("Evaluate failed: %v", err)
	}
	if len(violations) != 0 {
		t.Errorf("expected 0 violations, got %v", violations)
	}
}

// TestEvaluator_CrossPackageImport verifies that rego files in DIFFERENT
// packages can import each other using `import data.<package>`.
func TestEvaluator_CrossPackageImport(t *testing.T) {
	dir := t.TempDir()

	// File 1: utility package with reusable helpers
	utils := `
package utils
import rego.v1
is_confirm if { input.context.action == "confirm" }
is_high_value if { input.message.order.value > 10000 }
`
	os.WriteFile(filepath.Join(dir, "utils.rego"), []byte(utils), 0644)

	// File 2: policy package imports from utils package
	rules := `
package policy
import rego.v1
import data.utils
violations contains "high value confirm blocked" if {
    utils.is_confirm
    utils.is_high_value
}
`
	os.WriteFile(filepath.Join(dir, "rules.rego"), []byte(rules), 0644)

	eval, err := NewEvaluator([]string{dir}, "data.policy.violations", nil, false, 0, nil)
	if err != nil {
		t.Fatalf("NewEvaluator failed: %v", err)
	}

	// confirm + high value — should fire
	violations, err := eval.Evaluate(context.Background(), []byte(`{
		"context": {"action": "confirm"},
		"message": {"order": {"value": 50000}}
	}`))
	if err != nil {
		t.Fatalf("Evaluate failed: %v", err)
	}
	if len(violations) != 1 {
		t.Errorf("expected 1 violation, got %v", violations)
	}

	// search action — should NOT fire (action filter in rego)
	violations, err = eval.Evaluate(context.Background(), []byte(`{
		"context": {"action": "search"},
		"message": {"order": {"value": 50000}}
	}`))
	if err != nil {
		t.Fatalf("Evaluate failed: %v", err)
	}
	if len(violations) != 0 {
		t.Errorf("expected 0 violations for search action, got %v", violations)
	}
}

// TestEvaluator_MultiFileOrganization demonstrates a realistic modular layout
// where policies are organized by concern (compliance, safety, rate-limiting)
// across separate files that all work together.
func TestEvaluator_MultiFileOrganization(t *testing.T) {
	dir := t.TempDir()

	// Shared helpers
	helpers := `
package helpers
import rego.v1
action_is(a) if { input.context.action == a }
value_exceeds(limit) if { input.message.order.value > limit }
`
	os.WriteFile(filepath.Join(dir, "helpers.rego"), []byte(helpers), 0644)

	// compliance.rego — compliance rules
	compliance := `
package policy
import rego.v1
import data.helpers
violations contains "compliance: missing provider" if {
    helpers.action_is("confirm")
    not input.message.order.provider
}
`
	os.WriteFile(filepath.Join(dir, "compliance.rego"), []byte(compliance), 0644)

	// safety.rego — safety rules
	safety := `
package policy
import rego.v1
import data.helpers
violations contains "safety: order value too high" if {
    helpers.action_is("confirm")
    helpers.value_exceeds(100000)
}
`
	os.WriteFile(filepath.Join(dir, "safety.rego"), []byte(safety), 0644)

	eval, err := NewEvaluator([]string{dir}, "data.policy.violations", nil, false, 0, nil)
	if err != nil {
		t.Fatalf("NewEvaluator failed: %v", err)
	}

	// Input that triggers BOTH violations
	violations, err := eval.Evaluate(context.Background(), []byte(`{
		"context": {"action": "confirm"},
		"message": {"order": {"value": 999999}}
	}`))
	if err != nil {
		t.Fatalf("Evaluate failed: %v", err)
	}
	if len(violations) != 2 {
		t.Errorf("expected 2 violations (compliance+safety), got %d: %v", len(violations), violations)
	}
}

// --- Enforcer Integration Tests ---

func TestEnforcer_Compliant(t *testing.T) {
	policy := `
package policy
import rego.v1
violations contains "blocked" if { input.context.action == "confirm"; input.block }
`
	dir := writePolicyDir(t, "test.rego", policy)

	configPath := writeDefaultOnlyNetworkPolicyConfig(t, "type: dir\nlocation: "+dir+"\nquery: data.policy.violations\nactions: confirm\n")
	enforcer, err := New(context.Background(), map[string]string{
		"networkPolicyConfig": configPath,
	})
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}

	ctx := makeStepCtx("confirm", `{"context": {"action": "confirm"}, "block": false}`)
	err = enforcer.CheckPolicy(ctx)
	if err != nil {
		t.Errorf("expected nil error for compliant message, got: %v", err)
	}
}

func TestEnforcer_NonCompliant(t *testing.T) {
	policy := `
package policy
import rego.v1
violations contains "blocked" if { input.context.action == "confirm" }
`
	dir := writePolicyDir(t, "test.rego", policy)

	configPath := writeDefaultOnlyNetworkPolicyConfig(t, "type: dir\nlocation: "+dir+"\nquery: data.policy.violations\nactions: confirm\n")
	enforcer, err := New(context.Background(), map[string]string{
		"networkPolicyConfig": configPath,
	})
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}

	ctx := makeStepCtx("confirm", `{"context": {"action": "confirm"}}`)
	err = enforcer.CheckPolicy(ctx)
	if err == nil {
		t.Fatal("expected error for non-compliant message, got nil")
	}

	// Should be a BadReqErr
	if _, ok := err.(*model.BadReqErr); !ok {
		t.Errorf("expected *model.BadReqErr, got %T: %v", err, err)
	}
}

func TestEnforcer_SkipsNonMatchingAction(t *testing.T) {
	policy := `
package policy
import rego.v1
violations contains "blocked" if { true }
`
	dir := writePolicyDir(t, "test.rego", policy)

	configPath := writeDefaultOnlyNetworkPolicyConfig(t, "type: dir\nlocation: "+dir+"\nquery: data.policy.violations\nactions: confirm\n")
	enforcer, err := New(context.Background(), map[string]string{
		"networkPolicyConfig": configPath,
	})
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}

	// Non-compliant body, but action is "search" — not in configured actions
	ctx := makeStepCtx("search", `{"context": {"action": "search"}}`)
	err = enforcer.CheckPolicy(ctx)
	if err != nil {
		t.Errorf("expected nil for non-matching action, got: %v", err)
	}
}

func TestEnforcer_DisabledPlugin(t *testing.T) {
	policy := `
package policy
import rego.v1
violations contains "blocked" if { true }
`
	dir := writePolicyDir(t, "test.rego", policy)

	configPath := writeNetworkPolicyConfig(t, "networkPolicies:\n  default:\n    type: dir\n    location: "+dir+"\n    query: data.policy.violations\n    enabled: false\n")
	enforcer, err := New(context.Background(), map[string]string{
		"networkPolicyConfig": configPath,
	})
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}

	ctx := makeStepCtx("confirm", `{"context": {"action": "confirm"}}`)
	err = enforcer.CheckPolicy(ctx)
	if err != nil {
		t.Errorf("expected nil for disabled plugin, got: %v", err)
	}
}

// --- Enforcer with URL-sourced policy ---

func TestEnforcer_PolicyFromURL(t *testing.T) {
	policy := `
package policy
import rego.v1
violations contains "blocked" if { input.context.action == "confirm" }
`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(policy))
	}))
	defer srv.Close()

	configPath := writeDefaultOnlyNetworkPolicyConfig(t, "type: file\nlocation: "+srv.URL+"/block_confirm.rego\nquery: data.policy.violations\nactions: confirm\n")
	enforcer, err := New(context.Background(), map[string]string{
		"networkPolicyConfig": configPath,
	})
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}

	ctx := makeStepCtx("confirm", `{"context": {"action": "confirm"}}`)
	err = enforcer.CheckPolicy(ctx)
	if err == nil {
		t.Fatal("expected error from URL-sourced policy, got nil")
	}
	if _, ok := err.(*model.BadReqErr); !ok {
		t.Errorf("expected *model.BadReqErr, got %T", err)
	}
}

// --- Request Context Parsing Tests ---

func TestExtractActionFromPath_FromURL(t *testing.T) {
	action := extractActionFromPath("/bpp/caller/confirm")
	if action != "confirm" {
		t.Errorf("expected 'confirm', got %q", action)
	}
}

func TestParseRequestContext(t *testing.T) {
	reqCtx := parseRequestContext([]byte(`{"context":{"action":"select","networkId":"retail.network/production","bap_id":"bap.example.com","bppId":"bpp.example.com","message_id":"msg-1","transactionId":"txn-1","timestamp":"2026-04-21T10:00:00Z"}}`))
	if reqCtx.Action != "select" {
		t.Fatalf("expected action select, got %q", reqCtx.Action)
	}
	if reqCtx.NetworkID != "retail.network/production" {
		t.Fatalf("expected network ID retail.network/production, got %q", reqCtx.NetworkID)
	}
	if reqCtx.BAPID != "bap.example.com" {
		t.Fatalf("expected bap_id bap.example.com, got %q", reqCtx.BAPID)
	}
	if reqCtx.BPPID != "bpp.example.com" {
		t.Fatalf("expected bpp_id bpp.example.com, got %q", reqCtx.BPPID)
	}
	if reqCtx.MessageID != "msg-1" {
		t.Fatalf("expected message_id msg-1, got %q", reqCtx.MessageID)
	}
	if reqCtx.TransactionID != "txn-1" {
		t.Fatalf("expected transaction_id txn-1, got %q", reqCtx.TransactionID)
	}
	if reqCtx.Timestamp != "2026-04-21T10:00:00Z" {
		t.Fatalf("expected timestamp 2026-04-21T10:00:00Z, got %q", reqCtx.Timestamp)
	}
}

func TestParseRequestContext_NetworkIDVariants(t *testing.T) {
	if got := parseRequestContext([]byte(`{"context":{"networkId":"retail.network/production"}}`)).NetworkID; got != "retail.network/production" {
		t.Fatalf("expected camelCase networkId, got %q", got)
	}
	if got := parseRequestContext([]byte(`{"context":{"network_id":"retail.network/sandbox"}}`)).NetworkID; got != "retail.network/sandbox" {
		t.Fatalf("expected snake_case network_id, got %q", got)
	}
}

// --- Config Tests: Bundle Type ---

func TestParsePolicyConfig_BundleType(t *testing.T) {
	cfg, err := parsePolicyConfig(map[string]string{
		"type":     "bundle",
		"location": "https://example.com/bundle.tar.gz",
		"query":    "data.retail.validation.result",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !cfg.IsBundle {
		t.Error("expected IsBundle=true for type=bundle")
	}
	if len(cfg.PolicyPaths) != 1 || cfg.PolicyPaths[0] != "https://example.com/bundle.tar.gz" {
		t.Errorf("expected 1 policy path, got %v", cfg.PolicyPaths)
	}
	if cfg.Query != "data.retail.validation.result" {
		t.Errorf("expected query 'data.retail.validation.result', got %q", cfg.Query)
	}
}

func TestParsePolicyConfig_VerificationForDirRejected(t *testing.T) {
	_, err := parsePolicyConfig(map[string]string{
		"type":                            "dir",
		"location":                        "/tmp/policies",
		"query":                           "data.policy.result",
		"verification.enabled":            "true",
		"verification.publicKeyLookupUrl": "/tmp/public.pem",
		"verification.signatureLocation":  "/tmp/policies.sig",
	})
	if err == nil {
		t.Fatal("expected error when verification is enabled for dir")
	}
}

func TestParsePolicyConfig_VerificationForBundleRejectsSignatureLocation(t *testing.T) {
	_, err := parsePolicyConfig(map[string]string{
		"type":                            "bundle",
		"location":                        "/tmp/policies.tar.gz",
		"query":                           "data.policy.result",
		"verification.enabled":            "true",
		"verification.publicKeyLookupUrl": "/tmp/public.pem",
		"verification.signatureLocation":  "/tmp/policies.sig",
	})
	if err == nil {
		t.Fatal("expected error when signatureLocation is set for bundle verification")
	}
}

func TestParsePolicyConfig_URLTypeRejected(t *testing.T) {
	_, err := parsePolicyConfig(map[string]string{
		"type":     "url",
		"location": "https://example.com/policy.rego",
		"query":    "data.policy.result",
	})
	if err == nil {
		t.Fatal("expected error when type=url is used")
	}
}

func TestLoadNetworkPolicies_WithNestedVerificationBlock(t *testing.T) {
	configPath := writeNetworkPolicyConfig(t, `
networkPolicies:
  default:
    type: file
    location: ./policy.rego
    query: data.policy.result
    verification:
      enabled: true
      publicKeyLookupUrl: https://api.dedi.global/dedi/lookup/ns/public_key_test/policy-key
      signatureLocation: ./policy.rego.sig
`)

	policies, err := loadNetworkPolicies(configPath)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	cfg := policies["default"]
	if cfg["verification.enabled"] != "true" {
		t.Fatalf("expected verification.enabled=true, got %q", cfg["verification.enabled"])
	}
	if cfg["verification.publicKeyLookupUrl"] == "" {
		t.Fatal("expected verification.publicKeyLookupUrl to be flattened")
	}
	if cfg["verification.signatureLocation"] != "./policy.rego.sig" {
		t.Fatalf("unexpected verification.signatureLocation %q", cfg["verification.signatureLocation"])
	}
}

func TestLoadNetworkPolicies_RejectsUnexpectedNestedMap(t *testing.T) {
	configPath := writeNetworkPolicyConfig(t, `
networkPolicies:
  default:
    type: file
    location: ./policy.rego
    query: data.policy.result
    actions:
      confirm: true
`)

	_, err := loadNetworkPolicies(configPath)
	if err == nil {
		t.Fatal("expected nested non-verification map to be rejected")
	}
	if !strings.Contains(err.Error(), "must be a scalar value") {
		t.Fatalf("expected scalar value error, got %v", err)
	}
}

func TestParseVerificationPublicKeyResponse_DeDiBase64Key(t *testing.T) {
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("failed to generate RSA key: %v", err)
	}
	der, err := x509.MarshalPKIXPublicKey(&privateKey.PublicKey)
	if err != nil {
		t.Fatalf("failed to marshal public key: %v", err)
	}

	body := `{"data":{"details":{"keyType":"RSA","keyFormat":"base64","publicKey":"` + base64.StdEncoding.EncodeToString(der) + `"}}}`
	key, err := artifactverifier.ParsePublicKeyResponse([]byte(body))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := key.(*rsa.PublicKey); !ok {
		t.Fatalf("expected *rsa.PublicKey, got %T", key)
	}
}

// --- Structured Result Format Tests ---

func TestEvaluator_StructuredResult_Valid(t *testing.T) {
	// Policy returns {"valid": true, "violations": []} — no violations expected
	policy := `
package retail.policy

import rego.v1

default result := {
  "valid": true,
  "violations": []
}
`
	dir := writePolicyDir(t, "policy.rego", policy)
	eval, err := NewEvaluator([]string{dir}, "data.retail.policy.result", nil, false, 0, nil)
	if err != nil {
		t.Fatalf("NewEvaluator failed: %v", err)
	}

	violations, err := eval.Evaluate(context.Background(), []byte(`{"message": {"order": {"items": []}}}`))
	if err != nil {
		t.Fatalf("Evaluate failed: %v", err)
	}
	if len(violations) != 0 {
		t.Errorf("expected 0 violations for valid result, got %v", violations)
	}
}

func TestEvaluator_StructuredResult_WithViolations(t *testing.T) {
	// Policy returns {"valid": false, "violations": ["msg1", "msg2"]} when items have count <= 0
	policy := `
package retail.policy

import rego.v1

default result := {
  "valid": true,
  "violations": []
}

result := {
  "valid": count(violations) == 0,
  "violations": violations
}

violations contains msg if {
  some item in input.message.order.items
  item.quantity.count <= 0
  msg := sprintf("item %s: quantity must be > 0", [item.id])
}
`
	dir := writePolicyDir(t, "policy.rego", policy)
	eval, err := NewEvaluator([]string{dir}, "data.retail.policy.result", nil, false, 0, nil)
	if err != nil {
		t.Fatalf("NewEvaluator failed: %v", err)
	}

	// Non-compliant input
	body := `{
		"message": {
			"order": {
				"items": [
					{"id": "item1", "quantity": {"count": 0}},
					{"id": "item2", "quantity": {"count": 5}}
				]
			}
		}
	}`
	violations, err := eval.Evaluate(context.Background(), []byte(body))
	if err != nil {
		t.Fatalf("Evaluate failed: %v", err)
	}
	if len(violations) != 1 {
		t.Fatalf("expected 1 violation, got %d: %v", len(violations), violations)
	}
	if violations[0] != "item item1: quantity must be > 0" {
		t.Errorf("unexpected violation: %q", violations[0])
	}

	// Compliant input
	body = `{
		"message": {
			"order": {
				"items": [
					{"id": "item1", "quantity": {"count": 3}}
				]
			}
		}
	}`
	violations, err = eval.Evaluate(context.Background(), []byte(body))
	if err != nil {
		t.Fatalf("Evaluate failed: %v", err)
	}
	if len(violations) != 0 {
		t.Errorf("expected 0 violations for compliant input, got %v", violations)
	}
}

func TestEvaluator_StructuredResult_FalseNoViolations(t *testing.T) {
	// Edge case: valid=false but violations is empty — should report generic denial
	policy := `
package policy

import rego.v1

result := {
  "valid": false,
  "violations": []
}
`
	dir := writePolicyDir(t, "policy.rego", policy)
	eval, err := NewEvaluator([]string{dir}, "data.policy.result", nil, false, 0, nil)
	if err != nil {
		t.Fatalf("NewEvaluator failed: %v", err)
	}

	violations, err := eval.Evaluate(context.Background(), []byte(`{}`))
	if err != nil {
		t.Fatalf("Evaluate failed: %v", err)
	}
	if len(violations) != 1 || violations[0] != "policy denied the request" {
		t.Errorf("expected ['policy denied the request'], got %v", violations)
	}
}

func TestEvaluator_NonStructuredMapResult_Ignored(t *testing.T) {
	policy := `
package policy

import rego.v1

result := {
  "action": "confirm",
  "status": "ok"
}
`
	dir := writePolicyDir(t, "policy.rego", policy)
	eval, err := NewEvaluator([]string{dir}, "data.policy.result", nil, false, 0, nil)
	if err != nil {
		t.Fatalf("NewEvaluator failed: %v", err)
	}

	violations, err := eval.Evaluate(context.Background(), []byte(`{}`))
	if err != nil {
		t.Fatalf("Evaluate failed: %v", err)
	}
	if len(violations) != 0 {
		t.Fatalf("expected non-structured map result to be ignored, got %v", violations)
	}
}

// --- Bundle Tests ---

// buildTestBundle creates an OPA bundle .tar.gz in memory from the given modules.
func buildTestBundle(t *testing.T, modules map[string]string) []byte {
	t.Helper()
	b := bundle.Bundle{
		Modules: make([]bundle.ModuleFile, 0, len(modules)),
		Data:    make(map[string]interface{}),
	}
	for path, content := range modules {
		b.Modules = append(b.Modules, bundle.ModuleFile{
			URL:    path,
			Path:   path,
			Raw:    []byte(content),
			Parsed: nil,
		})
	}

	var buf bytes.Buffer
	if err := bundle.Write(&buf, b); err != nil {
		t.Fatalf("failed to write test bundle: %v", err)
	}
	return buf.Bytes()
}

func buildSignedTestBundle(t *testing.T, modules map[string]string) ([]byte, string) {
	t.Helper()

	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("failed to generate RSA key: %v", err)
	}

	privatePEM := pem.EncodeToMemory(&pem.Block{
		Type:  "PRIVATE KEY",
		Bytes: mustMarshalPKCS8PrivateKey(t, privateKey),
	})
	publicPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "PUBLIC KEY",
		Bytes: mustMarshalPKIXPublicKey(t, &privateKey.PublicKey),
	})

	b := bundle.Bundle{
		Modules: make([]bundle.ModuleFile, 0, len(modules)),
		Data:    make(map[string]interface{}),
	}
	for path, content := range modules {
		b.Modules = append(b.Modules, bundle.ModuleFile{
			URL:  path,
			Path: path,
			Raw:  []byte(content),
		})
	}

	if err := b.GenerateSignature(bundle.NewSigningConfig(string(privatePEM), "RS256", ""), defaultBundleVerificationKeyID, false); err != nil {
		t.Fatalf("failed to sign test bundle: %v", err)
	}

	var buf bytes.Buffer
	if err := bundle.Write(&buf, b); err != nil {
		t.Fatalf("failed to write signed test bundle: %v", err)
	}

	return buf.Bytes(), string(publicPEM)
}

func mustMarshalPKCS8PrivateKey(t *testing.T, key *rsa.PrivateKey) []byte {
	t.Helper()
	encoded, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatalf("failed to marshal private key: %v", err)
	}
	return encoded
}

func mustMarshalPKIXPublicKey(t *testing.T, key *rsa.PublicKey) []byte {
	t.Helper()
	encoded, err := x509.MarshalPKIXPublicKey(key)
	if err != nil {
		t.Fatalf("failed to marshal public key: %v", err)
	}
	return encoded
}

func signArtifactRSA(t *testing.T, content []byte) ([]byte, string) {
	t.Helper()

	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("failed to generate RSA key: %v", err)
	}

	sum := sha256.Sum256(content)
	signature, err := rsa.SignPKCS1v15(rand.Reader, privateKey, crypto.SHA256, sum[:])
	if err != nil {
		t.Fatalf("failed to sign artifact: %v", err)
	}

	publicPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "PUBLIC KEY",
		Bytes: mustMarshalPKIXPublicKey(t, &privateKey.PublicKey),
	})
	return signature, string(publicPEM)
}

func TestEvaluator_BundleFromURL(t *testing.T) {
	policy := `
package retail.validation

import rego.v1

default result := {
  "valid": true,
  "violations": []
}

result := {
  "valid": count(violations) == 0,
  "violations": violations
}

violations contains msg if {
  some item in input.message.order.items
  item.quantity.count <= 0
  msg := sprintf("item %s: quantity must be > 0", [item.id])
}
`
	bundleData := buildTestBundle(t, map[string]string{
		"retail/validation.rego": policy,
	})

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/gzip")
		w.Write(bundleData)
	}))
	defer srv.Close()

	eval, err := NewEvaluator([]string{srv.URL + "/bundle.tar.gz"}, "data.retail.validation.result", nil, true, 0, nil)
	if err != nil {
		t.Fatalf("NewEvaluator with bundle failed: %v", err)
	}

	body := `{"message":{"order":{"items":[{"id":"x","quantity":{"count":0}}]}}}`
	violations, err := eval.Evaluate(context.Background(), []byte(body))
	if err != nil {
		t.Fatalf("Evaluate failed: %v", err)
	}
	if len(violations) != 1 {
		t.Fatalf("expected 1 violation, got %d: %v", len(violations), violations)
	}

	body = `{"message":{"order":{"items":[{"id":"x","quantity":{"count":5}}]}}}`
	violations, err = eval.Evaluate(context.Background(), []byte(body))
	if err != nil {
		t.Fatalf("Evaluate failed: %v", err)
	}
	if len(violations) != 0 {
		t.Errorf("expected 0 violations, got %v", violations)
	}
}

func TestEvaluator_BundleFromLocalFile(t *testing.T) {
	policy := `
package retail.validation

import rego.v1

default result := {
  "valid": true,
  "violations": []
}
`
	bundleData := buildTestBundle(t, map[string]string{
		"retail/validation.rego": policy,
	})

	dir := t.TempDir()
	bundlePath := filepath.Join(dir, "policy-bundle.tar.gz")
	if err := os.WriteFile(bundlePath, bundleData, 0644); err != nil {
		t.Fatalf("failed to write bundle: %v", err)
	}

	eval, err := NewEvaluator([]string{bundlePath}, "data.retail.validation.result", nil, true, 0, nil)
	if err != nil {
		t.Fatalf("NewEvaluator with local bundle failed: %v", err)
	}

	violations, err := eval.Evaluate(context.Background(), []byte(`{"message":{"order":{"items":[]}}}`))
	if err != nil {
		t.Fatalf("Evaluate failed: %v", err)
	}
	if len(violations) != 0 {
		t.Fatalf("expected no violations, got %v", violations)
	}
}

func TestEvaluator_BundleVerificationFromLocalFile(t *testing.T) {
	policy := `
package retail.validation

import rego.v1

default result := {
  "valid": true,
  "violations": []
}
`
	bundleData, publicPEM := buildSignedTestBundle(t, map[string]string{
		"retail/validation.rego": policy,
	})

	dir := t.TempDir()
	bundlePath := filepath.Join(dir, "signed-bundle.tar.gz")
	publicKeyPath := filepath.Join(dir, "bundle-public.pem")
	if err := os.WriteFile(bundlePath, bundleData, 0644); err != nil {
		t.Fatalf("failed to write signed bundle: %v", err)
	}
	if err := os.WriteFile(publicKeyPath, []byte(publicPEM), 0644); err != nil {
		t.Fatalf("failed to write public key: %v", err)
	}

	eval, err := NewEvaluator(
		[]string{bundlePath},
		"data.retail.validation.result",
		nil,
		true,
		0,
		&ArtifactVerificationConfig{
			Enabled:            true,
			PublicKeyLookupURL: publicKeyPath,
			Algorithm:          "RS256",
		},
	)
	if err != nil {
		t.Fatalf("NewEvaluator with signed local bundle failed: %v", err)
	}

	violations, err := eval.Evaluate(context.Background(), []byte(`{"message":{"order":{"items":[]}}}`))
	if err != nil {
		t.Fatalf("Evaluate failed: %v", err)
	}
	if len(violations) != 0 {
		t.Fatalf("expected no violations, got %v", violations)
	}
}

func TestEvaluator_FileVerificationFromLocalFile(t *testing.T) {
	policy := []byte(`
package policy
import rego.v1
violations := set()
`)

	signature, publicPEM := signArtifactRSA(t, policy)

	dir := t.TempDir()
	policyPath := filepath.Join(dir, "policy.rego")
	signaturePath := filepath.Join(dir, "policy.rego.sig")
	publicKeyPath := filepath.Join(dir, "public.pem")
	if err := os.WriteFile(policyPath, policy, 0644); err != nil {
		t.Fatalf("failed to write policy: %v", err)
	}
	if err := os.WriteFile(signaturePath, signature, 0644); err != nil {
		t.Fatalf("failed to write signature: %v", err)
	}
	if err := os.WriteFile(publicKeyPath, []byte(publicPEM), 0644); err != nil {
		t.Fatalf("failed to write public key: %v", err)
	}

	eval, err := NewEvaluator(
		[]string{policyPath},
		"data.policy.violations",
		nil,
		false,
		0,
		&ArtifactVerificationConfig{
			Enabled:            true,
			PublicKeyLookupURL: publicKeyPath,
			SignatureLocation:  signaturePath,
		},
	)
	if err != nil {
		t.Fatalf("NewEvaluator with signed local file failed: %v", err)
	}

	violations, err := eval.Evaluate(context.Background(), []byte(`{}`))
	if err != nil {
		t.Fatalf("Evaluate failed: %v", err)
	}
	if len(violations) != 0 {
		t.Fatalf("expected no violations, got %v", violations)
	}
}

func TestEvaluator_FileVerificationWithDeDiHTTPPublicKeyLookup(t *testing.T) {
	policy := []byte(`
package policy
import rego.v1
violations := set()
`)

	signature, publicPEM := signArtifactRSA(t, policy)
	block, _ := pem.Decode([]byte(publicPEM))
	if block == nil {
		t.Fatal("failed to decode PEM public key")
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"data":{"details":{"keyType":"RSA","keyFormat":"base64","publicKey":"` + base64.StdEncoding.EncodeToString(block.Bytes) + `"}}}`))
	}))
	defer server.Close()

	dir := t.TempDir()
	policyPath := filepath.Join(dir, "policy.rego")
	signaturePath := filepath.Join(dir, "policy.rego.sig")
	if err := os.WriteFile(policyPath, policy, 0644); err != nil {
		t.Fatalf("failed to write policy: %v", err)
	}
	if err := os.WriteFile(signaturePath, signature, 0644); err != nil {
		t.Fatalf("failed to write signature: %v", err)
	}

	eval, err := NewEvaluator(
		[]string{policyPath},
		"data.policy.violations",
		nil,
		false,
		0,
		&ArtifactVerificationConfig{
			Enabled:            true,
			PublicKeyLookupURL: server.URL,
			SignatureLocation:  signaturePath,
		},
	)
	if err != nil {
		t.Fatalf("NewEvaluator with DeDi HTTP public key lookup failed: %v", err)
	}

	violations, err := eval.Evaluate(context.Background(), []byte(`{}`))
	if err != nil {
		t.Fatalf("Evaluate failed: %v", err)
	}
	if len(violations) != 0 {
		t.Fatalf("expected no violations, got %v", violations)
	}
}

func TestEnforcer_BundlePolicy(t *testing.T) {
	policy := `
package retail.policy

import rego.v1

default result := {
  "valid": true,
  "violations": []
}

result := {
  "valid": count(violations) == 0,
  "violations": violations
}

violations contains "blocked" if {
  input.context.action == "confirm"
  not input.message.order.provider
}
`
	bundleData := buildTestBundle(t, map[string]string{
		"retail/policy.rego": policy,
	})

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/gzip")
		w.Write(bundleData)
	}))
	defer srv.Close()

	configPath := writeDefaultOnlyNetworkPolicyConfig(t, "type: bundle\nlocation: "+srv.URL+"/policy-bundle.tar.gz\nquery: data.retail.policy.result\nactions: confirm\n")
	enforcer, err := New(context.Background(), map[string]string{
		"networkPolicyConfig": configPath,
	})
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}

	// Non-compliant: confirm without provider
	ctx := makeStepCtx("confirm", `{"context": {"action": "confirm"}, "message": {"order": {}}}`)
	err = enforcer.CheckPolicy(ctx)
	if err == nil {
		t.Fatal("expected error for non-compliant message, got nil")
	}
	if _, ok := err.(*model.BadReqErr); !ok {
		t.Errorf("expected *model.BadReqErr, got %T: %v", err, err)
	}

	// Compliant: confirm with provider
	ctx = makeStepCtx("confirm", `{"context": {"action": "confirm"}, "message": {"order": {"provider": {"id": "p1"}}}}`)
	err = enforcer.CheckPolicy(ctx)
	if err != nil {
		t.Errorf("expected nil error for compliant message, got: %v", err)
	}
}

func TestParseConfig_RefreshInterval(t *testing.T) {
	cfg, err := ParseConfig(map[string]string{
		"networkPolicyConfig": "/tmp/network-policies.yaml",
		"refreshInterval":     "20m",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.RefreshInterval != 20*time.Minute {
		t.Errorf("expected 20m refresh interval, got %v", cfg.RefreshInterval)
	}
}

func TestParseConfig_RefreshInterval_Zero(t *testing.T) {
	cfg, err := ParseConfig(map[string]string{
		"networkPolicyConfig": "/tmp/network-policies.yaml",
		// no refreshInterval -> disabled
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.RefreshInterval != 0 {
		t.Errorf("expected refresh disabled (0), got %v", cfg.RefreshInterval)
	}
}

func TestParseConfig_RefreshInterval_Invalid(t *testing.T) {
	_, err := ParseConfig(map[string]string{
		"networkPolicyConfig": "/tmp/network-policies.yaml",
		"refreshInterval":     "not-a-duration",
	})
	if err == nil {
		t.Fatal("expected error for invalid refreshInterval")
	}
}

// TestEnforcer_HotReload verifies that the hot-reload goroutine picks up changes
// to a local policy file within the configured refresh interval.
func TestEnforcer_HotReload(t *testing.T) {
	dir := t.TempDir()
	policyPath := filepath.Join(dir, "policy.rego")

	// Initial policy: always blocks confirm
	blockPolicy := `package policy
import rego.v1
default result := {"valid": false, "violations": ["blocked by initial policy"]}
result := {"valid": false, "violations": ["blocked by initial policy"]}
`
	if err := os.WriteFile(policyPath, []byte(blockPolicy), 0644); err != nil {
		t.Fatalf("failed to write initial policy: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	configPath := writeDefaultOnlyNetworkPolicyConfig(t, "type: dir\nlocation: "+dir+"\nquery: data.policy.result\n")
	enforcer, err := New(ctx, map[string]string{
		"networkPolicyConfig": configPath,
		"refreshInterval":     "1s",
	})
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}

	// Confirm is blocked with initial policy
	stepCtx := makeStepCtx("confirm", `{"context":{"action":"confirm"}}`)
	if err := enforcer.CheckPolicy(stepCtx); err == nil {
		t.Fatal("expected block from initial policy, got nil")
	}

	// Swap policy on disk to allow everything
	allowPolicy := `package policy
import rego.v1
default result := {"valid": true, "violations": []}
`
	if err := os.WriteFile(policyPath, []byte(allowPolicy), 0644); err != nil {
		t.Fatalf("failed to write updated policy: %v", err)
	}

	// Wait up to 5s for the reload to fire and swap the evaluator
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if err := enforcer.CheckPolicy(stepCtx); err == nil {
			// Reload took effect
			return
		}
		time.Sleep(200 * time.Millisecond)
	}

	t.Fatal("hot-reload did not take effect within 5 seconds")
}

func TestEnforcer_NetworkPolicySelection(t *testing.T) {
	retailDir := writePolicyDir(t, "retail.rego", `
package retail
import rego.v1
default result := {"valid": false, "violations": ["retail policy violation"]}
result := {"valid": false, "violations": ["retail policy violation"]}
`)
	logisticsDir := writePolicyDir(t, "logistics.rego", `
package logistics
import rego.v1
default result := {"valid": false, "violations": ["logistics policy violation"]}
result := {"valid": false, "violations": ["logistics policy violation"]}
`)

	configPath := writeNetworkPolicyConfig(t, `
networkPolicies:
  retail.network/production:
    type: dir
    location: `+retailDir+`
    query: data.retail.result
    actions: confirm
  retail.network/logistics:
    type: dir
    location: `+logisticsDir+`
    query: data.logistics.result
    actions: confirm
`)

	enforcer, err := New(context.Background(), map[string]string{
		"networkPolicyConfig": configPath,
	})
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}

	retailCtx := makeStepCtx("confirm", `{"context":{"action":"confirm","networkId":"retail.network/production"}}`)
	if err := enforcer.CheckPolicy(retailCtx); err == nil || !strings.Contains(err.Error(), "retail policy violation") {
		t.Fatalf("expected retail policy violation, got %v", err)
	}

	logisticsCtx := makeStepCtx("confirm", `{"context":{"action":"confirm","networkId":"retail.network/logistics"}}`)
	if err := enforcer.CheckPolicy(logisticsCtx); err == nil || !strings.Contains(err.Error(), "logistics policy violation") {
		t.Fatalf("expected logistics policy violation, got %v", err)
	}
}

func TestEnforcer_NetworkPolicyDefaultFallback(t *testing.T) {
	defaultDir := writePolicyDir(t, "default.rego", `
package policy
import rego.v1
default result := {"valid": false, "violations": ["default policy violation"]}
result := {"valid": false, "violations": ["default policy violation"]}
`)

	configPath := writeNetworkPolicyConfig(t, `
networkPolicies:
  default:
    type: dir
    location: `+defaultDir+`
    query: data.policy.result
    actions: confirm
`)

	enforcer, err := New(context.Background(), map[string]string{
		"networkPolicyConfig": configPath,
	})
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}

	ctx := makeStepCtx("confirm", `{"context":{"action":"confirm","networkId":"unknown.network/production"}}`)
	if err := enforcer.CheckPolicy(ctx); err == nil || !strings.Contains(err.Error(), "default policy violation") {
		t.Fatalf("expected default policy violation, got %v", err)
	}
}

func TestEnforcer_NetworkPolicyDisabledExactMatchOverridesDefault(t *testing.T) {
	defaultDir := writePolicyDir(t, "default.rego", `
package policy
import rego.v1
default result := {"valid": false, "violations": ["default policy violation"]}
result := {"valid": false, "violations": ["default policy violation"]}
`)

	configPath := writeNetworkPolicyConfig(t, `
networkPolicies:
  retail.network/production:
    enabled: false
    type: dir
    location: `+defaultDir+`
    query: data.policy.result
    actions: confirm
  default:
    type: dir
    location: `+defaultDir+`
    query: data.policy.result
    actions: confirm
`)

	enforcer, err := New(context.Background(), map[string]string{
		"networkPolicyConfig": configPath,
	})
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}

	ctx := makeStepCtx("confirm", `{"context":{"action":"confirm","networkId":"retail.network/production"}}`)
	if err := enforcer.CheckPolicy(ctx); err != nil {
		t.Fatalf("expected exact disabled network match to skip without falling back to default, got %v", err)
	}
}

func TestEnforcer_NetworkPolicyUnknownWithoutDefaultSkips(t *testing.T) {
	retailDir := writePolicyDir(t, "retail.rego", `
package retail
import rego.v1
default result := {"valid": false, "violations": ["retail policy violation"]}
result := {"valid": false, "violations": ["retail policy violation"]}
`)

	configPath := writeNetworkPolicyConfig(t, `
networkPolicies:
  retail.network/production:
    type: dir
    location: `+retailDir+`
    query: data.retail.result
    actions: confirm
`)

	enforcer, err := New(context.Background(), map[string]string{
		"networkPolicyConfig": configPath,
	})
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}

	ctx := makeStepCtx("confirm", `{"context":{"action":"confirm","networkId":"unknown.network/production"}}`)
	if err := enforcer.CheckPolicy(ctx); err != nil {
		t.Fatalf("expected unknown network with no default to skip, got %v", err)
	}
}

func TestEnforcer_NetworkPolicyStartupFailureIfAnyPolicyInvalid(t *testing.T) {
	retailDir := writePolicyDir(t, "retail.rego", `
package retail
import rego.v1
default result := {"valid": true, "violations": []}
`)

	configPath := writeNetworkPolicyConfig(t, `
networkPolicies:
  retail.network/production:
    type: dir
    location: `+retailDir+`
    query: data.retail.result
  retail.network/invalid:
    type: dir
    location: `+retailDir+`
`)

	if _, err := New(context.Background(), map[string]string{
		"networkPolicyConfig": configPath,
	}); err == nil {
		t.Fatal("expected startup failure when one network policy is invalid")
	}
}

func TestParsePolicyConfig_FetchTimeout(t *testing.T) {
	cfg, err := parsePolicyConfig(map[string]string{
		"type":                "file",
		"location":            "https://example.com/policy.rego",
		"query":               "data.policy.violations",
		"fetchTimeoutSeconds": "7",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.FetchTimeout != 7*time.Second {
		t.Fatalf("expected fetch timeout 7s, got %s", cfg.FetchTimeout)
	}
}

func TestEvaluator_FetchURL_Timeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(50 * time.Millisecond)
		w.Write([]byte(`package policy
import rego.v1
violations := []`))
	}))
	defer srv.Close()

	_, err := NewEvaluator([]string{srv.URL + "/slow.rego"}, "data.policy.violations", nil, false, 10*time.Millisecond, nil)
	if err == nil {
		t.Fatal("expected timeout error for slow policy URL")
	}
}

func TestExtractAction_NonStandardURLFallsBackToBody(t *testing.T) {
	reqCtx := parseRequestContext([]byte(`{"context": {"action": "confirm"}}`))
	action := extractActionFromPath("/bpp/caller/confirm/extra")
	if action == "" {
		action = reqCtx.Action
	}
	if action != "confirm" {
		t.Fatalf("expected body fallback action 'confirm', got %q", action)
	}
}

func TestEnforcer_ManifestBackedFilePolicy(t *testing.T) {
	policy := []byte(`
package policy
import rego.v1
default result := {"valid": true, "violations": []}
result := {"valid": false, "violations": ["missing provider"]} if {
  input.context.action == "confirm"
  not input.message.order.provider
}
`)
	signature, publicPEM := signArtifactRSA(t, policy)

	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/policy.rego":
			w.Write(policy)
		case "/policy.rego.sig":
			w.Write(signature)
		case "/public.pem":
			w.Write([]byte(publicPEM))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	manifest := strings.Join([]string{
		`manifest_version: "1.0"`,
		`manifest_type: "network-manifest"`,
		`network_id: "retail.network/production"`,
		`release_id: "2026.04"`,
		`publisher:`,
		`  role: "NFO"`,
		`  domain: "example.org"`,
		`policies:`,
		`  type: "rego"`,
		`  source: "file"`,
		`  file:`,
		`    id: "network-policy-file"`,
		`    url: "` + server.URL + `/policy.rego"`,
		`    policy_query_path: "data.policy.result"`,
		`    signed: true`,
		`    signature_url: "` + server.URL + `/policy.rego.sig"`,
		`    signing_public_key_lookup_url: "` + server.URL + `/public.pem"`,
		strings.TrimSuffix(validManifestGovernanceYAML(), "\n"),
	}, "\n") + "\n"

	configPath := writeNetworkPolicyConfig(t, `
networkPolicies:
  retail.network/production:
    type: manifest
    actions: confirm
`)
	enforcer, err := NewWithManifestLoader(context.Background(), stubManifestLoader{
		docs: map[string]*model.ManifestDocument{
			"retail.network/production": {Content: []byte(manifest), Verified: true},
		},
	}, map[string]string{
		"networkPolicyConfig": configPath,
	})
	if err != nil {
		t.Fatalf("NewWithManifestLoader failed: %v", err)
	}

	ctx := makeStepCtx("confirm", `{"context":{"action":"confirm","networkId":"retail.network/production"},"message":{"order":{}}}`)
	if err := enforcer.CheckPolicy(ctx); err == nil || !strings.Contains(err.Error(), "missing provider") {
		t.Fatalf("expected manifest-backed file violation, got %v", err)
	}
}

func TestEnforcer_ManifestBackedBundlePolicy(t *testing.T) {
	policy := `
package retail.policy
import rego.v1
default result := {"valid": true, "violations": []}
result := {"valid": false, "violations": ["bundle blocked"]} if {
  input.context.action == "confirm"
  not input.message.order.provider
}
`
	bundleData := buildTestBundle(t, map[string]string{
		"retail/policy.rego": policy,
	})

	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/policy-bundle.tar.gz":
			w.Header().Set("Content-Type", "application/gzip")
			w.Write(bundleData)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	manifest := strings.Join([]string{
		`manifest_version: "1.0"`,
		`manifest_type: "network-manifest"`,
		`network_id: "retail.network/production"`,
		`release_id: "2026.04"`,
		`publisher:`,
		`  role: "NFO"`,
		`  domain: "example.org"`,
		`policies:`,
		`  type: "rego"`,
		`  source: "bundle"`,
		`  bundle:`,
		`    id: "network-policy-bundle"`,
		`    url: "` + server.URL + `/policy-bundle.tar.gz"`,
		`    policy_query_path: "data.retail.policy.result"`,
		`    signed: false`,
		strings.TrimSuffix(validManifestGovernanceYAML(), "\n"),
	}, "\n") + "\n"

	configPath := writeNetworkPolicyConfig(t, `
networkPolicies:
  retail.network/production:
    type: manifest
    actions: confirm
`)
	enforcer, err := NewWithManifestLoader(context.Background(), stubManifestLoader{
		docs: map[string]*model.ManifestDocument{
			"retail.network/production": {Content: []byte(manifest), Verified: true},
		},
	}, map[string]string{
		"networkPolicyConfig": configPath,
	})
	if err != nil {
		t.Fatalf("NewWithManifestLoader failed: %v", err)
	}

	ctx := makeStepCtx("confirm", `{"context":{"action":"confirm","networkId":"retail.network/production"},"message":{"order":{}}}`)
	if err := enforcer.CheckPolicy(ctx); err == nil || !strings.Contains(err.Error(), "bundle blocked") {
		t.Fatalf("expected manifest-backed bundle violation, got %v", err)
	}
}

func TestEnforcer_ManifestPolicyRequiresLoader(t *testing.T) {
	configPath := writeNetworkPolicyConfig(t, `
networkPolicies:
  retail.network/production:
    type: manifest
`)

	if _, err := New(context.Background(), map[string]string{
		"networkPolicyConfig": configPath,
	}); err == nil || !strings.Contains(err.Error(), "ManifestLoader") {
		t.Fatalf("expected manifest loader requirement error, got %v", err)
	}
}

func TestEnforcer_ManifestPolicyMissingPoliciesSection(t *testing.T) {
	manifest := strings.Join([]string{
		`manifest_version: "1.0"`,
		`manifest_type: "network-manifest"`,
		`network_id: "retail.network/production"`,
		`release_id: "2026.04"`,
		`publisher:`,
		`  role: "NFO"`,
		`  domain: "example.org"`,
		strings.TrimSuffix(validManifestGovernanceYAML(), "\n"),
	}, "\n") + "\n"

	configPath := writeNetworkPolicyConfig(t, `
networkPolicies:
  retail.network/production:
    type: manifest
`)
	if _, err := NewWithManifestLoader(context.Background(), stubManifestLoader{
		docs: map[string]*model.ManifestDocument{
			"retail.network/production": {Content: []byte(manifest), Verified: true},
		},
	}, map[string]string{
		"networkPolicyConfig": configPath,
	}); err == nil || !strings.Contains(err.Error(), "missing policies section") {
		t.Fatalf("expected missing policies error, got %v", err)
	}
}

func TestEnforcer_ManifestPolicyUnsupportedSource(t *testing.T) {
	manifest := strings.Join([]string{
		`manifest_version: "1.0"`,
		`manifest_type: "network-manifest"`,
		`network_id: "retail.network/production"`,
		`release_id: "2026.04"`,
		`publisher:`,
		`  role: "NFO"`,
		`  domain: "example.org"`,
		`policies:`,
		`  type: "rego"`,
		`  source: "dir"`,
		strings.TrimSuffix(validManifestGovernanceYAML(), "\n"),
	}, "\n") + "\n"

	configPath := writeNetworkPolicyConfig(t, `
networkPolicies:
  retail.network/production:
    type: manifest
`)
	if _, err := NewWithManifestLoader(context.Background(), stubManifestLoader{
		docs: map[string]*model.ManifestDocument{
			"retail.network/production": {Content: []byte(manifest), Verified: true},
		},
	}, map[string]string{
		"networkPolicyConfig": configPath,
	}); err == nil || !strings.Contains(err.Error(), `unsupported policies.source "dir"`) {
		t.Fatalf("expected unsupported source error, got %v", err)
	}
}

func TestEnforcer_ManifestPolicyNetworkMismatch(t *testing.T) {
	manifest := strings.Join([]string{
		`manifest_version: "1.0"`,
		`manifest_type: "network-manifest"`,
		`network_id: "retail.network/sandbox"`,
		`release_id: "2026.04"`,
		`publisher:`,
		`  role: "NFO"`,
		`  domain: "example.org"`,
		`policies:`,
		`  type: "rego"`,
		`  source: "file"`,
		`  file:`,
		`    id: "network-policy-file"`,
		`    url: "https://example.org/policy.rego"`,
		`    policy_query_path: "data.policy.result"`,
		`    signed: false`,
		strings.TrimSuffix(validManifestGovernanceYAML(), "\n"),
	}, "\n") + "\n"

	configPath := writeNetworkPolicyConfig(t, `
networkPolicies:
  retail.network/production:
    type: manifest
`)
	if _, err := NewWithManifestLoader(context.Background(), stubManifestLoader{
		docs: map[string]*model.ManifestDocument{
			"retail.network/production": {Content: []byte(manifest), Verified: true},
		},
	}, map[string]string{
		"networkPolicyConfig": configPath,
	}); err == nil || !strings.Contains(err.Error(), "does not match configured network") {
		t.Fatalf("expected network mismatch error, got %v", err)
	}
}

func TestEnforcer_ManifestPolicyExpired(t *testing.T) {
	manifest := strings.Join([]string{
		`manifest_version: "1.0"`,
		`manifest_type: "network-manifest"`,
		`network_id: "retail.network/production"`,
		`release_id: "2026.04"`,
		`publisher:`,
		`  role: "NFO"`,
		`  domain: "example.org"`,
		`policies:`,
		`  type: "rego"`,
		`  source: "file"`,
		`  file:`,
		`    id: "network-policy-file"`,
		`    url: "https://example.org/policy.rego"`,
		`    policy_query_path: "data.policy.result"`,
		`    signed: false`,
		fmt.Sprintf("governance:\n  effective_from: %q\n  effective_until: %q\n  signed: true\n",
			time.Now().UTC().Add(-2*time.Hour).Format(time.RFC3339),
			time.Now().UTC().Add(-1*time.Hour).Format(time.RFC3339),
		),
	}, "\n")

	configPath := writeNetworkPolicyConfig(t, `
networkPolicies:
  retail.network/production:
    type: manifest
`)
	if _, err := NewWithManifestLoader(context.Background(), stubManifestLoader{
		docs: map[string]*model.ManifestDocument{
			"retail.network/production": {Content: []byte(manifest), Verified: true},
		},
	}, map[string]string{
		"networkPolicyConfig": configPath,
	}); err == nil || !strings.Contains(err.Error(), "expired") {
		t.Fatalf("expected expired manifest error, got %v", err)
	}
}

func TestEnforcer_ManifestLoaderError(t *testing.T) {
	configPath := writeNetworkPolicyConfig(t, `
networkPolicies:
  retail.network/production:
    type: manifest
`)

	if _, err := NewWithManifestLoader(context.Background(), stubManifestLoader{
		err: errors.New("lookup failed"),
	}, map[string]string{
		"networkPolicyConfig": configPath,
	}); err == nil || !strings.Contains(err.Error(), "lookup failed") {
		t.Fatalf("expected manifest loader error, got %v", err)
	}
}

func TestEnforcer_DisabledSkipsEvaluatorInitialization(t *testing.T) {
	enforcer, err := New(context.Background(), map[string]string{
		"networkPolicyConfig": "/tmp/any.yaml",
		"enabled":             "false",
	})
	if err != nil {
		t.Fatalf("expected disabled enforcer to skip evaluator initialization, got %v", err)
	}
	if len(enforcer.policies) != 0 || enforcer.defaultPolicy != nil {
		t.Fatal("expected disabled enforcer to skip policy initialization")
	}
}
