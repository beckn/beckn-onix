// End-to-end verification runner: resolves the manifest and baseline, walks
// the local devkit, verifies every applicable role, evaluates the deployment
// policy, and (optionally) emits deviation telemetry. This is the single
// entry point the CLI calls, kept in the library so the whole flow is
// testable without a process boundary.
package deployconform

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"time"

	"github.com/beckn-one/beckn-onix/pkg/model"
)

// defaultTimeout bounds remote fetches when the caller does not set one.
const defaultTimeout = 30 * time.Second

// VerifyOptions configures one verification run.
type VerifyOptions struct {
	// Root is the devkit checkout directory.
	Root string
	// ComposePath overrides the baseline's composePath (root-relative).
	ComposePath string
	// Role selects a single baseline role; "" verifies every role that has
	// at least one of its services in the local compose file.
	Role string
	// Source selects how the network manifest is resolved. Ignored when
	// BaselineOverride is set.
	Source ManifestSource
	// BaselineOverride supplies a local baseline document, bypassing the
	// manifest entirely (development use only).
	BaselineOverride []byte
	// PolicyOverride supplies a local deployment policy with its query,
	// bypassing the manifest's deployment.policy (development use only).
	PolicyOverride *PolicyOverride
	// CollectorURL overrides the manifest's observability collector.
	CollectorURL string
	// EmitTelemetry sends deviation events to the collector when true.
	EmitTelemetry bool
	// Timeout bounds each remote fetch; defaults to defaultTimeout.
	Timeout time.Duration
}

// PolicyOverride is a locally supplied deployment policy.
type PolicyOverride struct {
	Source string
	Query  string
}

// VerifyResult is the outcome of one verification run across all verified
// roles, plus the warnings produced along the way (telemetry failures and
// other non-fatal conditions).
type VerifyResult struct {
	Reports  []*Report
	Warnings []string
}

// Compliant reports whether every verified role matched the baseline.
func (r *VerifyResult) Compliant() bool {
	for _, report := range r.Reports {
		if !report.Compliant() {
			return false
		}
	}
	return true
}

// RunVerification executes a full verification pass per opts. Deviations are
// reported in the result, never as an error: the library only reports;
// enforcement (startup gating via --strict or --status-file) is composed by
// the caller.
func RunVerification(ctx context.Context, opts VerifyOptions) (*VerifyResult, error) {
	if opts.Timeout <= 0 {
		opts.Timeout = defaultTimeout
	}
	opts.Source.Timeout = opts.Timeout
	client := &http.Client{Timeout: opts.Timeout}

	baseline, manifest, baselineDigest, err := resolveBaseline(ctx, client, opts)
	if err != nil {
		return nil, err
	}

	composePath := opts.ComposePath
	if composePath == "" {
		composePath = baseline.ComposePath
	}
	devkit, err := LoadDevkit(opts.Root, composePath)
	if err != nil {
		return nil, err
	}

	policySource, policyQuery, err := resolvePolicy(ctx, client, manifest, opts)
	if err != nil {
		return nil, err
	}

	roles, err := selectRoles(devkit, baseline, opts.Role)
	if err != nil {
		return nil, err
	}

	// Compile the deployment policy once, up front, so a broken policy fails
	// the run before any role is verified.
	var evaluator *PolicyEvaluator
	if policySource != "" {
		if evaluator, err = NewPolicyEvaluator(ctx, policySource, policyQuery); err != nil {
			return nil, err
		}
	}

	result := &VerifyResult{}
	for _, roleName := range roles {
		// One discovery per role: the hash comparison and the policy input
		// must see the same snapshot of the checkout.
		artifacts, err := devkit.RoleArtifacts(baseline.Roles[roleName].Services, baseline.Include)
		if err != nil {
			return nil, fmt.Errorf("discover local artifacts for role %q: %w", roleName, err)
		}
		report, err := VerifyRoleArtifacts(baseline, roleName, artifacts)
		if err != nil {
			return nil, err
		}
		report.BaselineDigest = baselineDigest

		if evaluator != nil {
			violations, err := evaluator.Evaluate(ctx, BuildPolicyInput(devkit, baseline, roleName, artifacts))
			if err != nil {
				return nil, err
			}
			if len(violations) > 0 {
				details := make([]FindingDetail, 0, len(violations))
				for _, violation := range violations {
					details = append(details, FindingDetail{Message: violation})
				}
				report.Findings = append(report.Findings, Finding{Kind: FindingPolicy, Details: details})
			}
		}
		result.Reports = append(result.Reports, report)
	}

	if opts.EmitTelemetry {
		result.Warnings = append(result.Warnings, emitDeviations(ctx, client, manifest, opts, result.Reports)...)
	}
	return result, nil
}

// resolveBaseline obtains the baseline document: from the local override, or
// through the network manifest's deployment section with signature
// verification. It returns the parsed baseline, the manifest when one was
// resolved, and the baseline's SHA-256 digest for report correlation.
func resolveBaseline(ctx context.Context, client *http.Client, opts VerifyOptions) (*Baseline, *model.NetworkManifest, string, error) {
	var content []byte
	var manifest *model.NetworkManifest
	expectedNetwork, expectedDevkit := "", ""

	if len(opts.BaselineOverride) > 0 {
		content = opts.BaselineOverride
	} else {
		var err error
		manifest, err = ResolveManifest(ctx, opts.Source)
		if err != nil {
			return nil, nil, "", err
		}
		if manifest.Deployment == nil {
			return nil, nil, "", fmt.Errorf("manifest for network %q has no deployment section", manifest.NetworkID)
		}
		content, err = FetchVerifiedArtifact(ctx, client, manifest.Deployment.Baseline, opts.Source.SkipSignatureVerification)
		if err != nil {
			return nil, nil, "", err
		}
		expectedNetwork = manifest.NetworkID
		expectedDevkit = manifest.Deployment.DevkitID
	}

	baseline, err := ParseBaseline(content)
	if err != nil {
		return nil, nil, "", err
	}
	if err := baseline.Validate(expectedNetwork, expectedDevkit); err != nil {
		return nil, nil, "", err
	}
	return baseline, manifest, sha256Hex(content), nil
}

// resolvePolicy obtains the deployment policy source and query: from the
// local override, or fetched (and signature-verified) per the manifest's
// deployment.policy section. An empty source means no policy is configured.
// Only single-file Rego policies are supported; bundle distribution for
// deployment policies is not implemented yet.
func resolvePolicy(ctx context.Context, client *http.Client, manifest *model.NetworkManifest, opts VerifyOptions) (source, query string, err error) {
	if opts.PolicyOverride != nil {
		return opts.PolicyOverride.Source, opts.PolicyOverride.Query, nil
	}
	if manifest == nil || manifest.Deployment == nil || manifest.Deployment.Policy == nil {
		return "", "", nil
	}
	policy := manifest.Deployment.Policy
	if policy.Source != model.PolicySourceFile {
		return "", "", fmt.Errorf("deployment.policy.source %q is not supported yet (only \"file\")", policy.Source)
	}
	content, err := FetchVerifiedArtifact(ctx, client, policyFileToArtifactRef(policy.File), opts.Source.SkipSignatureVerification)
	if err != nil {
		return "", "", err
	}
	return string(content), policy.File.PolicyQueryPath, nil
}

// selectRoles decides which baseline roles to verify: the explicitly
// requested one, or every role with at least one service present in the
// local compose file — so a participant deploying only its own slice is not
// flooded with findings about roles it never runs, while a partially
// deployed role is still fully checked.
func selectRoles(devkit *Devkit, baseline *Baseline, requested string) ([]string, error) {
	if requested != "" {
		if _, ok := baseline.Roles[requested]; !ok {
			return nil, fmt.Errorf("baseline does not define role %q", requested)
		}
		return []string{requested}, nil
	}
	var roles []string
	for name, role := range baseline.Roles {
		for _, service := range role.Services {
			if devkit.HasService(service) {
				roles = append(roles, name)
				break
			}
		}
	}
	if len(roles) == 0 {
		return nil, fmt.Errorf("no baseline role matches any service in the local compose file")
	}
	sort.Strings(roles)
	return roles, nil
}

// emitDeviations sends one telemetry event per non-compliant role to the
// resolved collector. Telemetry is best-effort: failures come back as
// warnings, never abort the verification.
func emitDeviations(ctx context.Context, client *http.Client, manifest *model.NetworkManifest, opts VerifyOptions, reports []*Report) []string {
	collector := opts.CollectorURL
	if collector == "" && manifest != nil && manifest.Observability != nil &&
		manifest.Observability.Enabled && manifest.Observability.Collector != nil {
		collector = manifest.Observability.Collector.URL
	}
	if collector == "" {
		return []string{"telemetry requested but no collector is configured (manifest observability.collector.url or --collector-url)"}
	}
	var warnings []string
	now := time.Now().UTC()
	for _, report := range reports {
		if report.Compliant() {
			continue
		}
		if err := EmitDeviation(ctx, client, collector, NewDeviationEvent(report, now)); err != nil {
			warnings = append(warnings, fmt.Sprintf("emit deviation event for role %q: %v", report.Role, err))
		}
	}
	return warnings
}
