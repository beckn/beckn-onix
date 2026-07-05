// Command deployconform verifies a deployed devkit configuration against the
// network's published deployment baseline, and generates that baseline for
// network facilitators.
//
// Usage:
//
//	deployconform baseline --root <devkit> --spec <spec.yaml> [--out <file>]
//	deployconform verify   --root <devkit> --network-id <ns/registry> [flags]
//
// Verification is warn-only by default: deviations are reported (and
// optionally emitted to the network's observability collector) but the exit
// code stays 0 unless --strict is given. Run with --watch to keep verifying
// on an interval, e.g. as a sidecar container next to the devkit stack.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/beckn-one/beckn-onix/pkg/deployconform"
	"github.com/beckn-one/beckn-onix/pkg/model"
)

const (
	// defaultDediBaseURL is the DeDi API base used to resolve registry
	// metadata when verifying by network ID.
	defaultDediBaseURL = "https://api.dedi.global/dedi"
	// minWatchInterval is the smallest allowed --watch interval; each tick
	// re-fetches the manifest and its artifacts from shared infrastructure.
	minWatchInterval = time.Minute
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: deployconform <baseline|verify> [flags]")
		os.Exit(1)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	switch os.Args[1] {
	case "baseline":
		runBaseline(ctx, os.Args[2:])
	case "verify":
		runVerify(ctx, os.Args[2:])
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q — expected baseline or verify\n", os.Args[1])
		os.Exit(1)
	}
}

// runBaseline generates a baseline document from a facilitator's reference
// devkit checkout and a baseline spec (identity, variance rules, roles).
// The output must then be signed and published per the network manifest docs.
func runBaseline(_ context.Context, args []string) {
	fs := flag.NewFlagSet("baseline", flag.ExitOnError)
	root := fs.String("root", ".", "devkit root directory")
	compose := fs.String("compose", "", "compose file path relative to root (overrides the spec's composePath)")
	spec := fs.String("spec", "", "baseline spec YAML (required)")
	out := fs.String("out", "", "output file (default: stdout)")
	_ = fs.Parse(args)

	if *spec == "" {
		fatalf("baseline: --spec is required")
	}
	specContent, err := os.ReadFile(*spec)
	if err != nil {
		fatalf("read spec: %v", err)
	}
	baseline, err := deployconform.ParseBaseline(specContent)
	if err != nil {
		fatalf("parse spec: %v", err)
	}
	if *compose != "" {
		baseline.ComposePath = *compose
	}

	devkit, err := deployconform.LoadDevkit(*root, baseline.ComposePath)
	if err != nil {
		fatalf("load devkit: %v", err)
	}
	generated, err := deployconform.GenerateBaseline(devkit, baseline)
	if err != nil {
		fatalf("generate baseline: %v", err)
	}
	encoded, err := yaml.Marshal(generated)
	if err != nil {
		fatalf("encode baseline: %v", err)
	}

	if *out == "" {
		fmt.Print(string(encoded))
		return
	}
	if err := os.WriteFile(*out, encoded, 0o644); err != nil {
		fatalf("write baseline: %v", err)
	}
	fmt.Fprintf(os.Stderr, "baseline written to %s — sign it (e.g. openssl dgst -sha256 -sign) and publish it with the network manifest\n", *out)
}

// runVerify verifies the local devkit against the network baseline, once or
// on an interval (--watch). Exit codes: 0 = ran (deviations are warnings),
// 1 = execution error, 2 = deviations found and --strict was set.
func runVerify(ctx context.Context, args []string) {
	fs := flag.NewFlagSet("verify", flag.ExitOnError)
	root := fs.String("root", ".", "devkit root directory")
	compose := fs.String("compose", "", "compose file path relative to root (overrides the baseline's composePath)")
	role := fs.String("role", "", "verify a single baseline role (default: every role with services in the local compose file)")

	networkID := fs.String("network-id", "", "resolve the network manifest via DeDi for <namespace>/<registryName>")
	dediURL := fs.String("dedi-url", defaultDediBaseURL, "DeDi API base URL used with --network-id")
	manifestURL := fs.String("manifest-url", "", "fetch the network manifest from this URL")
	manifestSigURL := fs.String("manifest-signature-url", "", "detached signature URL for --manifest-url")
	manifestKeyURL := fs.String("manifest-key-url", "", "signing public key lookup URL for --manifest-url")
	manifestFile := fs.String("manifest-file", "", "read the network manifest from a local file (UNVERIFIED — development only)")
	baselineFile := fs.String("baseline-file", "", "read the baseline from a local file, bypassing the manifest (development only)")
	policyFile := fs.String("policy-file", "", "read the deployment policy from a local Rego file (development only)")
	policyQuery := fs.String("policy-query", "data.deployment.policy.result", "query path used with --policy-file")

	collectorURL := fs.String("collector-url", "", "override the manifest's observability collector URL")
	telemetry := fs.Bool("telemetry", true, "emit deviation events to the observability collector")
	watch := fs.Duration("watch", 0, "re-verify on this interval (e.g. 15m); 0 verifies once")
	strict := fs.Bool("strict", false, "exit with code 2 when deviations are found")
	jsonOut := fs.Bool("json", false, "print reports as JSON instead of text")
	timeout := fs.Duration("timeout", 30*time.Second, "timeout for each remote fetch")
	skipVerify := fs.Bool("skip-signature-verification", false, "skip artifact signature verification (NEVER use in production)")
	_ = fs.Parse(args)

	opts, err := buildOptions(verifyFlags{
		root: *root, compose: *compose, role: *role,
		networkID: *networkID, dediURL: *dediURL,
		manifestURL: *manifestURL, manifestSigURL: *manifestSigURL, manifestKeyURL: *manifestKeyURL,
		manifestFile: *manifestFile, baselineFile: *baselineFile,
		policyFile: *policyFile, policyQuery: *policyQuery,
		collectorURL: *collectorURL, telemetry: *telemetry,
		timeout: *timeout, skipVerify: *skipVerify,
	})
	if err != nil {
		fatalf("verify: %v", err)
	}
	if *skipVerify {
		fmt.Fprintln(os.Stderr, "WARN: signature verification is DISABLED — artifact authenticity is NOT guaranteed; do not use in production")
	}
	if *watch > 0 && *watch < minWatchInterval {
		// Every tick re-fetches the manifest and its artifacts from network
		// infrastructure the participant does not own; a floor keeps a typo
		// from turning the sidecar into a request storm.
		fmt.Fprintf(os.Stderr, "WARN: --watch below %s is clamped to %s\n", minWatchInterval, minWatchInterval)
		*watch = minWatchInterval
	}

	for {
		compliant := verifyOnce(ctx, *opts, *jsonOut, *watch <= 0)
		if *watch <= 0 {
			if !compliant && *strict {
				os.Exit(2)
			}
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(*watch):
		}
	}
}

// verifyFlags carries the parsed verify-command flags by name, so the
// assembly in buildOptions cannot silently transpose two of the many string
// parameters.
type verifyFlags struct {
	root, compose, role                         string
	networkID, dediURL                          string
	manifestURL, manifestSigURL, manifestKeyURL string
	manifestFile, baselineFile                  string
	policyFile, policyQuery                     string
	collectorURL                                string
	telemetry                                   bool
	timeout                                     time.Duration
	skipVerify                                  bool
}

// buildOptions validates the flag combination and assembles VerifyOptions.
// Exactly one manifest source is required unless a local baseline bypasses
// the manifest entirely.
func buildOptions(f verifyFlags) (*deployconform.VerifyOptions, error) {
	sources := 0
	for _, set := range []bool{f.networkID != "", f.manifestURL != "", f.manifestFile != ""} {
		if set {
			sources++
		}
	}
	if f.baselineFile == "" && sources != 1 {
		return nil, fmt.Errorf("exactly one of --network-id, --manifest-url, or --manifest-file is required (or --baseline-file to bypass the manifest)")
	}

	opts := &deployconform.VerifyOptions{
		Root:        f.root,
		ComposePath: f.compose,
		Role:        f.role,
		Source: deployconform.ManifestSource{
			NetworkID:                 f.networkID,
			DediBaseURL:               f.dediURL,
			SkipSignatureVerification: f.skipVerify,
		},
		CollectorURL:  f.collectorURL,
		EmitTelemetry: f.telemetry,
		Timeout:       f.timeout,
	}
	if f.manifestURL != "" {
		opts.Source.Metadata = model.ManifestMetadata{
			ManifestURL:               f.manifestURL,
			ManifestSignatureURL:      f.manifestSigURL,
			SigningPublicKeyLookupURL: f.manifestKeyURL,
		}
	}
	if f.manifestFile != "" {
		content, err := os.ReadFile(f.manifestFile)
		if err != nil {
			return nil, fmt.Errorf("read manifest file: %w", err)
		}
		opts.Source.LocalFile = content
	}
	if f.baselineFile != "" {
		content, err := os.ReadFile(f.baselineFile)
		if err != nil {
			return nil, fmt.Errorf("read baseline file: %w", err)
		}
		opts.BaselineOverride = content
	}
	if f.policyFile != "" {
		content, err := os.ReadFile(f.policyFile)
		if err != nil {
			return nil, fmt.Errorf("read policy file: %w", err)
		}
		opts.PolicyOverride = &deployconform.PolicyOverride{Source: string(content), Query: f.policyQuery}
	}
	return opts, nil
}

// verifyOnce runs a single verification pass and renders the result,
// returning whether the deployment was fully compliant. Execution errors are
// fatal in one-shot mode; in watch mode they are printed and the sidecar
// keeps running for the next tick.
func verifyOnce(ctx context.Context, opts deployconform.VerifyOptions, jsonOut, oneShot bool) bool {
	result, err := deployconform.RunVerification(ctx, opts)
	if err != nil {
		if ctx.Err() != nil {
			return false
		}
		if oneShot {
			fatalf("ERROR: %v", err)
		}
		fmt.Fprintf(os.Stderr, "ERROR: %v\n", err)
		return false
	}
	if jsonOut {
		encoded, err := json.MarshalIndent(result, "", "  ")
		if err != nil {
			fatalf("encode result: %v", err)
		}
		fmt.Println(string(encoded))
		return result.Compliant()
	}
	printResult(os.Stdout, os.Stderr, result)
	return result.Compliant()
}

// printResult renders one verification result as human-readable text: one OK
// or WARN block per role on out, findings indented beneath, and run warnings
// on errOut.
func printResult(out, errOut io.Writer, result *deployconform.VerifyResult) {
	for _, report := range result.Reports {
		if report.Compliant() {
			fmt.Fprintf(out, "OK   role %q conforms to the network baseline (root %.12s…)\n", report.Role, report.ComputedRoot)
			printRenames(out, report)
			continue
		}
		fmt.Fprintf(out, "WARN role %q deviates from the network baseline (expected root %.12s…, computed %.12s…)\n",
			report.Role, report.ExpectedRoot, report.ComputedRoot)
		printRenames(out, report)
		for _, finding := range report.Findings {
			switch finding.Kind {
			case deployconform.FindingPolicy:
				fmt.Fprintf(out, "  [policy] deployment policy violations:\n")
			default:
				fmt.Fprintf(out, "  [%s] %s\n", finding.Kind, finding.ArtifactID)
			}
			for _, detail := range finding.Details {
				fmt.Fprintf(out, "    - %s\n", detail.String())
			}
		}
	}
	for _, warning := range result.Warnings {
		fmt.Fprintf(errOut, "WARN: %s\n", warning)
	}
}

// printRenames lists content-conformant file renames beneath a role line.
// Renames are informational: they never affect compliance.
func printRenames(out io.Writer, report *deployconform.Report) {
	for _, rename := range report.Renames {
		fmt.Fprintf(out, "  [renamed] %s → %s\n", rename.From, rename.To)
	}
}

// fatalf prints an error to stderr and exits with code 1.
func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}
