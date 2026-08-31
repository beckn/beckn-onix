package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/beckn-one/beckn-onix/pkg/deployconform"
)

// TestBuildOptions checks manifest-source flag validation and file loading.
func TestBuildOptions(t *testing.T) {
	dir := t.TempDir()
	manifestFile := filepath.Join(dir, "manifest.yaml")
	baselineFile := filepath.Join(dir, "baseline.yaml")
	policyFile := filepath.Join(dir, "policy.rego")
	for _, f := range []string{manifestFile, baselineFile, policyFile} {
		if err := os.WriteFile(f, []byte("content"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	// build wraps buildOptions with defaults so each case only names what it
	// exercises.
	build := func(networkID, manifestURL, manifestFile, baselineFile, policyFile string) error {
		_, err := buildOptions(verifyFlags{
			root: ".", networkID: networkID, dediURL: defaultDediBaseURL,
			manifestURL: manifestURL, manifestFile: manifestFile,
			baselineFile: baselineFile, policyFile: policyFile, policyQuery: "data.q",
			telemetry: true, timeout: time.Second,
		})
		return err
	}

	if err := build("ns/reg", "", "", "", ""); err != nil {
		t.Fatalf("network-id source: %v", err)
	}
	if err := build("", "https://x/m.yaml", "", "", ""); err != nil {
		t.Fatalf("manifest-url source: %v", err)
	}
	if err := build("", "", manifestFile, "", policyFile); err != nil {
		t.Fatalf("manifest-file source: %v", err)
	}
	if err := build("", "", "", baselineFile, ""); err != nil {
		t.Fatalf("baseline-file source: %v", err)
	}
	if err := build("", "", "", "", ""); err == nil || !strings.Contains(err.Error(), "exactly one of") {
		t.Fatalf("no source must fail, got %v", err)
	}
	if err := build("ns/reg", "https://x/m.yaml", "", "", ""); err == nil {
		t.Fatalf("two sources must fail")
	}
	if err := build("", "", filepath.Join(dir, "missing.yaml"), "", ""); err == nil {
		t.Fatalf("missing manifest file must fail")
	}

	// Loaded content must land in the options.
	opts, err := buildOptions(verifyFlags{
		root: ".", role: "role1", dediURL: defaultDediBaseURL,
		baselineFile: baselineFile, policyFile: policyFile, policyQuery: "data.q",
		collectorURL: "https://c/events", telemetry: false, timeout: time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	if string(opts.BaselineOverride) != "content" || opts.PolicyOverride.Source != "content" ||
		opts.PolicyOverride.Query != "data.q" || opts.Role != "role1" ||
		opts.CollectorURL != "https://c/events" || opts.EmitTelemetry {
		t.Fatalf("options not wired correctly: %+v", opts)
	}
}

// TestPrintResult checks the text rendering of compliant and deviating
// reports, including finding details and run warnings.
func TestPrintResult(t *testing.T) {
	result := &deployconform.VerifyResult{
		Reports: []*deployconform.Report{
			{Role: "bap", ExpectedRoot: "aaaaaaaaaaaaaaaa", ComputedRoot: "aaaaaaaaaaaaaaaa"},
			{
				Role: "bpp", ExpectedRoot: "aaaaaaaaaaaaaaaa", ComputedRoot: "bbbbbbbbbbbbbbbb",
				Findings: []deployconform.Finding{
					{ArtifactID: "config/x.yaml", Kind: deployconform.FindingModified,
						Details: []deployconform.FindingDetail{{Path: "a.b", Message: "expected 1, got 2"}}},
					{Kind: deployconform.FindingPolicy,
						Details: []deployconform.FindingDetail{{Message: "checkPolicy step is required"}}},
				},
			},
		},
		Warnings: []string{"telemetry emit failed"},
	}
	var out, errOut strings.Builder
	printResult(&out, &errOut, result)

	for _, want := range []string{
		`OK   role "bap"`,
		`WARN role "bpp"`,
		"[modified] config/x.yaml",
		"- a.b: expected 1, got 2",
		"[policy] deployment policy violations:",
		"- checkPolicy step is required",
	} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("stdout missing %q:\n%s", want, out.String())
		}
	}
	if !strings.Contains(errOut.String(), "telemetry emit failed") {
		t.Fatalf("stderr missing warning: %s", errOut.String())
	}
}

// TestUpdateStatusFile checks the healthcheck marker lifecycle: created on a
// compliant pass, removed on deviation, inert with no path configured.
func TestUpdateStatusFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "conformant")

	updateStatusFile(path, true)
	content, err := os.ReadFile(path)
	if err != nil || len(content) == 0 {
		t.Fatalf("compliant pass must create the marker with a timestamp, got %q, %v", content, err)
	}

	updateStatusFile(path, false)
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("deviation must remove the marker, stat err = %v", err)
	}

	updateStatusFile(path, false) // removing an absent marker is fine
	updateStatusFile("", true)    // no path configured is a no-op
}
