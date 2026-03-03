package policyenforcer

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/beckn-one/beckn-onix/pkg/model"
)

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

// --- Config Tests ---

func TestParseConfig_RequiresPolicySource(t *testing.T) {
	_, err := ParseConfig(map[string]string{})
	if err == nil {
		t.Fatal("expected error when no policyPaths given")
	}
}

func TestParseConfig_Defaults(t *testing.T) {
	cfg, err := ParseConfig(map[string]string{"policyPaths": "/tmp"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Query != "data.policy.violations" {
		t.Errorf("expected default query 'data.policy.violations', got %q", cfg.Query)
	}
	if len(cfg.Actions) != 0 {
		t.Errorf("expected empty default actions (all enabled), got %v", cfg.Actions)
	}
	if !cfg.Enabled {
		t.Error("expected enabled=true by default")
	}
}

func TestParseConfig_RuntimeConfigForwarding(t *testing.T) {
	cfg, err := ParseConfig(map[string]string{
		"policyPaths":          "/tmp",
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

func TestParseConfig_CustomActions(t *testing.T) {
	cfg, err := ParseConfig(map[string]string{
		"policyPaths": "/tmp",
		"actions":     "confirm, select, init",
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

func TestParseConfig_PolicyPaths(t *testing.T) {
	cfg, err := ParseConfig(map[string]string{
		"policyPaths": "https://example.com/a.rego, https://example.com/b.rego",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cfg.PolicyPaths) != 2 {
		t.Fatalf("expected 2 paths, got %d: %v", len(cfg.PolicyPaths), cfg.PolicyPaths)
	}
	if cfg.PolicyPaths[0] != "https://example.com/a.rego" {
		t.Errorf("path[0] = %q", cfg.PolicyPaths[0])
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
	eval, err := NewEvaluator([]string{dir}, "data.policy.violations", nil)
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
	eval, err := NewEvaluator([]string{dir}, "data.policy.violations", nil)
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
	eval, err := NewEvaluator([]string{dir}, "data.policy.violations", map[string]string{"maxValue": "100"})
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

	eval, err := NewEvaluator([]string{dir}, "data.policy.violations", nil)
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
	eval, err := NewEvaluator([]string{dir}, "data.policy.violations", nil)
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

	eval, err := NewEvaluator([]string{srv.URL + "/test_policy.rego"}, "data.policy.violations", nil)
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

	_, err := NewEvaluator([]string{srv.URL + "/missing.rego"}, "data.policy.violations", nil)
	if err == nil {
		t.Fatal("expected error for 404 URL")
	}
}

func TestEvaluator_FetchURL_InvalidScheme(t *testing.T) {
	_, err := NewEvaluator([]string{"ftp://example.com/policy.rego"}, "data.policy.violations", nil)
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

	eval, err := NewEvaluator([]string{dir, srv.URL + "/remote.rego"}, "data.policy.violations", nil)
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

	eval, err := NewEvaluator([]string{policyPath}, "data.policy.violations", nil)
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

// --- Enforcer Integration Tests ---

func TestEnforcer_Compliant(t *testing.T) {
	policy := `
package policy
import rego.v1
violations contains "blocked" if { input.context.action == "confirm"; input.block }
`
	dir := writePolicyDir(t, "test.rego", policy)

	enforcer, err := New(map[string]string{
		"policyPaths": dir,
		"query":       "data.policy.violations",
		"actions":     "confirm",
	})
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}

	ctx := makeStepCtx("confirm", `{"context": {"action": "confirm"}, "block": false}`)
	err = enforcer.Run(ctx)
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

	enforcer, err := New(map[string]string{
		"policyPaths": dir,
		"query":       "data.policy.violations",
		"actions":     "confirm",
	})
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}

	ctx := makeStepCtx("confirm", `{"context": {"action": "confirm"}}`)
	err = enforcer.Run(ctx)
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

	enforcer, err := New(map[string]string{
		"policyPaths": dir,
		"query":       "data.policy.violations",
		"actions":     "confirm",
	})
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}

	// Non-compliant body, but action is "search" — not in configured actions
	ctx := makeStepCtx("search", `{"context": {"action": "search"}}`)
	err = enforcer.Run(ctx)
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

	enforcer, err := New(map[string]string{
		"policyPaths": dir,
		"query":       "data.policy.violations",
		"enabled":     "false",
	})
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}

	ctx := makeStepCtx("confirm", `{"context": {"action": "confirm"}}`)
	err = enforcer.Run(ctx)
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

	enforcer, err := New(map[string]string{
		"policyPaths": srv.URL + "/block_confirm.rego",
		"query":       "data.policy.violations",
		"actions":     "confirm",
	})
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}

	ctx := makeStepCtx("confirm", `{"context": {"action": "confirm"}}`)
	err = enforcer.Run(ctx)
	if err == nil {
		t.Fatal("expected error from URL-sourced policy, got nil")
	}
	if _, ok := err.(*model.BadReqErr); !ok {
		t.Errorf("expected *model.BadReqErr, got %T", err)
	}
}

// --- extractAction Tests ---

func TestExtractAction_FromURL(t *testing.T) {
	action := extractAction("/bpp/caller/confirm", nil)
	if action != "confirm" {
		t.Errorf("expected 'confirm', got %q", action)
	}
}

func TestExtractAction_FromBody(t *testing.T) {
	body := []byte(`{"context": {"action": "select"}}`)
	action := extractAction("/x", body)
	if action != "select" {
		t.Errorf("expected 'select', got %q", action)
	}
}
