package deployconform

import (
	"context"
	"strings"
	"testing"
)

// testDeploymentPolicy requires every adapter config to keep checkPolicy in
// each module's pipeline steps — the canonical "don't unwire policy
// enforcement" rule.
const testDeploymentPolicy = `package deployment.policy

import rego.v1

violations contains msg if {
	some name, cfg in input.artifacts
	startswith(name, "config/adapter-")
	some module in cfg.modules
	not "checkPolicy" in module.handler.steps
	msg := sprintf("%s: module %s must include the checkPolicy step", [name, module.name])
}

result := {"valid": count(violations) == 0, "violations": violations}
`

// TestBuildPolicyInput checks the input document shape: identity fields, the
// compose tree, parsed trees for structured artifacts, and text for raw ones.
func TestBuildPolicyInput(t *testing.T) {
	devkit := testDevkit(t)
	baseline := generateTestBaseline(t)
	artifacts, err := devkit.RoleArtifacts([]string{"onix-alpha"}, nil)
	if err != nil {
		t.Fatal(err)
	}

	input := BuildPolicyInput(devkit, baseline, "alpha", artifacts)
	if input["networkId"] != "example.org/testnet" || input["role"] != "alpha" || input["devkitId"] != "mini-devkit" {
		t.Fatalf("identity fields wrong: %+v", input)
	}
	if _, ok := input["compose"].(map[string]any); !ok {
		t.Fatalf("compose tree missing")
	}
	trees := input["artifacts"].(map[string]any)
	if _, ok := trees["config/adapter-alpha.yaml"].(map[string]any); !ok {
		t.Fatalf("structured artifact should be a parsed tree")
	}
	if _, ok := trees["policies/network.rego"].(string); !ok {
		t.Fatalf("raw artifact should be a string")
	}
}

// TestEvaluatePolicy exercises the decision-contract shapes and fail-closed
// behavior on the real deployment policy.
func TestEvaluatePolicy(t *testing.T) {
	ctx := context.Background()
	devkit := testDevkit(t)
	baseline := generateTestBaseline(t)
	artifacts, err := devkit.RoleArtifacts([]string{"onix-alpha"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	compliantInput := BuildPolicyInput(devkit, baseline, "alpha", artifacts)

	// Compliant input: adapter-alpha has checkPolicy in its steps.
	violations, err := EvaluatePolicy(ctx, testDeploymentPolicy, "data.deployment.policy.result", compliantInput)
	if err != nil || len(violations) != 0 {
		t.Fatalf("expected no violations, got %v, err %v", violations, err)
	}

	// Non-compliant input: strip the checkPolicy step.
	tampered := deepCopy(compliantInput).(map[string]any)
	adapter := tampered["artifacts"].(map[string]any)["config/adapter-alpha.yaml"].(map[string]any)
	module := adapter["modules"].([]any)[0].(map[string]any)
	module["handler"].(map[string]any)["steps"] = []any{"validateSign", "addRoute"}
	violations, err = EvaluatePolicy(ctx, testDeploymentPolicy, "data.deployment.policy.result", tampered)
	if err != nil || len(violations) != 1 || !strings.Contains(violations[0], "checkPolicy") {
		t.Fatalf("expected one checkPolicy violation, got %v, err %v", violations, err)
	}

	// Wrong query path: fail-closed, never silently compliant.
	violations, err = EvaluatePolicy(ctx, testDeploymentPolicy, "data.deployment.policy.nonexistent", compliantInput)
	if err != nil || len(violations) == 0 {
		t.Fatalf("undefined result must fail closed, got %v, err %v", violations, err)
	}

	// Compile error surfaces as an error.
	if _, err := EvaluatePolicy(ctx, "package broken\nresult :=", "data.broken.result", compliantInput); err == nil {
		t.Fatalf("expected compile error")
	}
}

// TestEvaluatePolicyResultShapes checks the alternative decision shapes the
// contract accepts: bare violation lists and bare booleans.
func TestEvaluatePolicyResultShapes(t *testing.T) {
	ctx := context.Background()
	input := map[string]any{"x": 1}

	tests := []struct {
		name           string
		policy         string
		query          string
		wantViolations int
	}{
		{
			name:           "bare list with violation",
			policy:         "package p\nimport rego.v1\nviolations contains \"bad\" if input.x == 1",
			query:          "data.p.violations",
			wantViolations: 1,
		},
		{
			name:           "bare bool deny",
			policy:         "package p\nimport rego.v1\nallow := false",
			query:          "data.p.allow",
			wantViolations: 1,
		},
		{
			name:           "bare bool allow",
			policy:         "package p\nimport rego.v1\nallow := true",
			query:          "data.p.allow",
			wantViolations: 0,
		},
		{
			name:           "structured deny without messages",
			policy:         "package p\nimport rego.v1\nresult := {\"valid\": false, \"violations\": []}",
			query:          "data.p.result",
			wantViolations: 1,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			violations, err := EvaluatePolicy(ctx, tt.policy, tt.query, input)
			if err != nil {
				t.Fatalf("EvaluatePolicy: %v", err)
			}
			if len(violations) != tt.wantViolations {
				t.Fatalf("violations = %v, want %d", violations, tt.wantViolations)
			}
		})
	}
}
