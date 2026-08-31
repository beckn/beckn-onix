package deployconform

import (
	"context"
	"crypto/ed25519"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

// collectorRecorder records deviation events POSTed to a test collector.
type collectorRecorder struct {
	mu     sync.Mutex
	events []DeviationEvent
	bodies []string
}

// server starts an httptest collector that records every event.
func (c *collectorRecorder) server(t *testing.T) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var event DeviationEvent
		if err := json.Unmarshal(body, &event); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		c.mu.Lock()
		c.events = append(c.events, event)
		c.bodies = append(c.bodies, string(body))
		c.mu.Unlock()
	}))
	t.Cleanup(server.Close)
	return server
}

// runnerFixture wires a complete verification environment: a devkit copy, a
// generated baseline served over HTTP (optionally signed), a deployment
// policy, a collector, and a manifest that ties them together.
type runnerFixture struct {
	root      string
	manifest  []byte
	collector *collectorRecorder
}

// newRunnerFixture builds the fixture. When priv is non-nil the served
// baseline is signed and the manifest marks it signed.
func newRunnerFixture(t *testing.T, priv ed25519.PrivateKey, pubPEM []byte) *runnerFixture {
	t.Helper()
	baseline := generateTestBaseline(t)
	encoded, err := yaml.Marshal(baseline)
	if err != nil {
		t.Fatal(err)
	}

	routes := map[string][]byte{
		"/baseline.yaml":   encoded,
		"/deployment.rego": []byte(testDeploymentPolicy),
	}
	sigURL, keyURL := "", ""
	if priv != nil {
		routes["/baseline.yaml.sig"] = ed25519.Sign(priv, encoded)
		routes["/public-key"] = pubPEM
	}
	server := artifactServer(t, routes)
	if priv != nil {
		sigURL = server.URL + "/baseline.yaml.sig"
		keyURL = server.URL + "/public-key"
	}

	collector := &collectorRecorder{}
	collectorServer := collector.server(t)

	return &runnerFixture{
		root:      copyDevkit(t),
		collector: collector,
		manifest: testManifestYAML(
			server.URL+"/baseline.yaml", sigURL, keyURL,
			server.URL+"/deployment.rego", collectorServer.URL+"/events"),
	}
}

// options returns VerifyOptions for the fixture's devkit and manifest.
func (f *runnerFixture) options() VerifyOptions {
	return VerifyOptions{
		Root:          f.root,
		Source:        ManifestSource{LocalFile: f.manifest},
		EmitTelemetry: true,
		Timeout:       5 * time.Second,
	}
}

// TestRunVerificationCompliant checks the full pipeline on a pristine
// checkout: every role compliant, no telemetry emitted.
func TestRunVerificationCompliant(t *testing.T) {
	fixture := newRunnerFixture(t, nil, nil)
	result, err := RunVerification(context.Background(), fixture.options())
	if err != nil {
		t.Fatalf("RunVerification: %v", err)
	}
	if !result.Compliant() || len(result.Reports) != 2 {
		t.Fatalf("expected 2 compliant reports, got %+v", result.Reports)
	}
	if len(result.Warnings) != 0 {
		t.Fatalf("unexpected warnings: %v", result.Warnings)
	}
	if len(fixture.collector.events) != 0 {
		t.Fatalf("compliant run must not emit telemetry, got %d events", len(fixture.collector.events))
	}
	for _, report := range result.Reports {
		if report.BaselineDigest == "" {
			t.Fatalf("report missing baseline digest")
		}
	}
}

// TestRunVerificationDeviation tampers with the devkit and checks that the
// hash layer, the policy layer, and the telemetry channel all fire — and
// that no local configuration value leaks into the emitted event.
func TestRunVerificationDeviation(t *testing.T) {
	fixture := newRunnerFixture(t, nil, nil)

	adapter := filepath.Join(fixture.root, "config", "adapter-alpha.yaml")
	content, _ := os.ReadFile(adapter)
	edited := strings.Replace(string(content), "        - checkPolicy\n", "", 1)
	edited = strings.ReplaceAll(edited, "debug", "sekretvalue")
	if err := os.WriteFile(adapter, []byte(edited), 0o644); err != nil {
		t.Fatal(err)
	}

	result, err := RunVerification(context.Background(), fixture.options())
	if err != nil {
		t.Fatalf("RunVerification: %v", err)
	}
	if result.Compliant() {
		t.Fatal("tampered devkit must not be compliant")
	}

	var alpha *Report
	for _, report := range result.Reports {
		if report.Role == "alpha" {
			alpha = report
		}
	}
	if alpha == nil || alpha.Compliant() {
		t.Fatalf("alpha role must deviate: %+v", result.Reports)
	}
	if _, ok := findingByKind(alpha, FindingModified); !ok {
		t.Fatalf("expected modified finding, got %+v", alpha.Findings)
	}
	policy, ok := findingByKind(alpha, FindingPolicy)
	if !ok || !strings.Contains(renderDetails(policy), "checkPolicy") {
		t.Fatalf("expected checkPolicy policy violation, got %+v", alpha.Findings)
	}

	// Exactly one deviation event (beta stays compliant), with paths but no
	// local values.
	if len(fixture.collector.events) != 1 {
		t.Fatalf("expected 1 telemetry event, got %d", len(fixture.collector.events))
	}
	event := fixture.collector.events[0]
	if event.Role != "alpha" || event.EventType != DeviationEventType {
		t.Fatalf("unexpected event: %+v", event)
	}
	if strings.Contains(fixture.collector.bodies[0], "sekretvalue") {
		t.Fatal("telemetry event leaked a local configuration value")
	}
}

// TestRunVerificationSignedBaseline checks the signed-baseline path
// end-to-end, and that a bad signature aborts the run.
func TestRunVerificationSignedBaseline(t *testing.T) {
	priv, pubPEM := signingKeys(t)
	fixture := newRunnerFixture(t, priv, pubPEM)
	result, err := RunVerification(context.Background(), fixture.options())
	if err != nil {
		t.Fatalf("RunVerification with signed baseline: %v", err)
	}
	if !result.Compliant() {
		t.Fatalf("expected compliant result, got %+v", result.Reports)
	}

	// A manifest pointing at a signature that does not match must abort.
	otherPriv, _ := signingKeys(t)
	badFixture := newRunnerFixture(t, otherPriv, pubPEM)
	if _, err := RunVerification(context.Background(), badFixture.options()); err == nil ||
		!strings.Contains(err.Error(), "signature verification failed") {
		t.Fatalf("expected signature failure, got %v", err)
	}
}

// TestRunVerificationBaselineOverride checks the manifest-free development
// path with a single-role filter.
func TestRunVerificationBaselineOverride(t *testing.T) {
	baseline := generateTestBaseline(t)
	encoded, err := yaml.Marshal(baseline)
	if err != nil {
		t.Fatal(err)
	}
	opts := VerifyOptions{
		Root:             filepath.Join("testdata", "devkit"),
		Role:             "beta",
		BaselineOverride: encoded,
		Timeout:          5 * time.Second,
	}
	result, err := RunVerification(context.Background(), opts)
	if err != nil {
		t.Fatalf("RunVerification: %v", err)
	}
	if len(result.Reports) != 1 || result.Reports[0].Role != "beta" || !result.Compliant() {
		t.Fatalf("expected one compliant beta report, got %+v", result.Reports)
	}

	opts.Role = "gamma"
	if _, err := RunVerification(context.Background(), opts); err == nil || !strings.Contains(err.Error(), `role "gamma"`) {
		t.Fatalf("expected unknown-role error, got %v", err)
	}
}

// TestRunVerificationNoDeploymentSection checks the clear error when the
// manifest lacks a deployment section.
func TestRunVerificationNoDeploymentSection(t *testing.T) {
	manifest := []byte(`manifestVersion: "1.0"
manifestType: network-manifest
networkId: example.org/testnet
releaseId: "2026.07"
publisher:
  role: NFO
  domain: example.org
policies:
  type: rego
  source: file
  file:
    id: message-policy
    url: https://example.org/network.rego
    policyQueryPath: data.test.policy.result
    signed: false
governance:
  effectiveFrom: "2020-01-01T00:00:00Z"
  signed: false
`)
	opts := VerifyOptions{
		Root:   filepath.Join("testdata", "devkit"),
		Source: ManifestSource{LocalFile: manifest},
	}
	if _, err := RunVerification(context.Background(), opts); err == nil || !strings.Contains(err.Error(), "no deployment section") {
		t.Fatalf("expected no-deployment-section error, got %v", err)
	}
}

// TestSelectRoles checks the auto role selection: only roles with at least
// one locally present service are verified.
func TestSelectRoles(t *testing.T) {
	devkit := testDevkit(t)
	baseline := generateTestBaseline(t)
	baseline.Roles["remote-only"] = &BaselineRole{Services: []string{"not-here"}}

	roles, err := selectRoles(devkit, baseline, "")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(roles, ",") != "alpha,beta" {
		t.Fatalf("selectRoles = %v, want [alpha beta]", roles)
	}

	baseline.Roles = map[string]*BaselineRole{"remote-only": {Services: []string{"not-here"}}}
	if _, err := selectRoles(devkit, baseline, ""); err == nil {
		t.Fatalf("expected error when no role matches")
	}
}

// TestEmitDeviationsWarnings checks that telemetry failures surface as
// warnings, not errors, and that a missing collector is reported.
func TestEmitDeviationsWarnings(t *testing.T) {
	report := &Report{Role: "alpha", ExpectedRoot: "a", ComputedRoot: "b"}
	client := &http.Client{Timeout: time.Second}

	warnings := emitDeviations(context.Background(), client, nil, VerifyOptions{}, []*Report{report})
	if len(warnings) != 1 || !strings.Contains(warnings[0], "no collector") {
		t.Fatalf("expected no-collector warning, got %v", warnings)
	}

	opts := VerifyOptions{CollectorURL: "http://127.0.0.1:1/events"}
	warnings = emitDeviations(context.Background(), client, nil, opts, []*Report{report})
	if len(warnings) != 1 || !strings.Contains(warnings[0], "emit deviation event") {
		t.Fatalf("expected emit warning, got %v", warnings)
	}
}
