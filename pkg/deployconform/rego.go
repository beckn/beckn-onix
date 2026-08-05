// Deployment-policy evaluation. Beyond the hash comparison (which pins every
// network-fixed value), the facilitator may publish a Rego policy that
// constrains the participant-owned values the baseline redacts — e.g.
// "allowedNetworkIDs must include this network" or "checkPolicy must appear
// in every module's steps". The policy is evaluated over the full, unredacted
// configuration tree and follows the same decision contract as network
// message policies: a rule returning {"valid": bool, "violations": [string]}.
package deployconform

import (
	"context"
	"fmt"

	"github.com/open-policy-agent/opa/v1/rego"
)

// BuildPolicyInput assembles the Rego input document for one role: network
// and devkit identity, the full compose tree, and every discovered artifact
// keyed by ID (parsed trees for structured artifacts, text for raw ones).
// The input is unredacted — evaluation is local, nothing leaves the host.
func BuildPolicyInput(devkit *Devkit, baseline *Baseline, roleName string, artifacts []Artifact) map[string]any {
	trees := make(map[string]any, len(artifacts))
	for _, a := range artifacts {
		if a.Kind == KindRaw {
			trees[a.ID] = string(a.Raw)
		} else {
			trees[a.ID] = a.Tree
		}
	}
	return map[string]any{
		"networkId": baseline.NetworkID,
		"devkitId":  baseline.DevkitID,
		"role":      roleName,
		"compose":   devkit.ComposeTree(),
		"artifacts": trees,
	}
}

// PolicyEvaluator is a compiled deployment policy, prepared once and
// evaluated against many inputs (one per role).
type PolicyEvaluator struct {
	prepared rego.PreparedEvalQuery
}

// NewPolicyEvaluator compiles a single Rego module and prepares query for
// evaluation. Compile errors surface here, before any role is verified.
func NewPolicyEvaluator(ctx context.Context, source, query string) (*PolicyEvaluator, error) {
	prepared, err := rego.New(
		rego.Query(query),
		rego.Module("deployment_policy.rego", source),
	).PrepareForEval(ctx)
	if err != nil {
		return nil, fmt.Errorf("compile deployment policy: %w", err)
	}
	return &PolicyEvaluator{prepared: prepared}, nil
}

// Evaluate runs the prepared policy against input, returning the policy's
// violation messages. An empty result means the input is compliant.
// Undefined or unrecognized results are fail-closed: they surface as a
// violation instead of silently passing.
func (e *PolicyEvaluator) Evaluate(ctx context.Context, input any) ([]string, error) {
	results, err := e.prepared.Eval(ctx, rego.EvalInput(input))
	if err != nil {
		return nil, fmt.Errorf("evaluate deployment policy: %w", err)
	}
	return extractViolations(results)
}

// EvaluatePolicy is the one-shot convenience form of NewPolicyEvaluator +
// Evaluate, for callers with a single input.
func EvaluatePolicy(ctx context.Context, source, query string, input any) ([]string, error) {
	evaluator, err := NewPolicyEvaluator(ctx, source, query)
	if err != nil {
		return nil, err
	}
	return evaluator.Evaluate(ctx, input)
}

// extractViolations interprets a Rego result set under the network policy
// decision contract (mirroring the opapolicychecker plugin's semantics).
// Supported shapes: {"valid": bool, "violations": [...]}, a bare list of
// violation strings, a bare boolean, or a bare string (non-empty = one
// violation). Anything else — including an undefined result — is treated as
// non-compliant so a broken query path can never masquerade as a clean
// report.
func extractViolations(results rego.ResultSet) ([]string, error) {
	if len(results) == 0 || len(results[0].Expressions) == 0 {
		return []string{"deployment policy produced no result (check policyQueryPath)"}, nil
	}
	switch value := results[0].Expressions[0].Value.(type) {
	case map[string]any:
		valid, _ := value["valid"].(bool)
		rawList, _ := value["violations"].([]any)
		violations := make([]string, 0, len(rawList))
		for _, item := range rawList {
			if s, ok := item.(string); ok {
				violations = append(violations, s)
			}
		}
		if !valid && len(violations) == 0 {
			violations = append(violations, "deployment policy denied the configuration")
		}
		return violations, nil
	case []any:
		violations := make([]string, 0, len(value))
		for _, item := range value {
			if s, ok := item.(string); ok {
				violations = append(violations, s)
			}
		}
		return violations, nil
	case bool:
		if !value {
			return []string{"deployment policy denied the configuration"}, nil
		}
		return nil, nil
	case string:
		if value != "" {
			return []string{value}, nil
		}
		return nil, nil
	default:
		return nil, fmt.Errorf("deployment policy returned unsupported result type %T", value)
	}
}
