package deployconform

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/beckn-one/beckn-onix/pkg/model"
)

// signingKeys generates an Ed25519 keypair and returns the private key plus
// the PEM-encoded public key payload served at key lookup URLs.
func signingKeys(t *testing.T) (ed25519.PrivateKey, []byte) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKIXPublicKey(pub)
	if err != nil {
		t.Fatal(err)
	}
	return priv, pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der})
}

// artifactServer serves a set of paths with fixed bodies.
func artifactServer(t *testing.T, routes map[string][]byte) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, ok := routes[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(body)
	}))
	t.Cleanup(server.Close)
	return server
}

// testManifestYAML renders a minimal valid network manifest whose deployment
// baseline (and optional policy) point at the given URLs.
func testManifestYAML(baselineURL, baselineSigURL, keyURL, policyURL, collectorURL string) []byte {
	var b strings.Builder
	b.WriteString(`manifestVersion: "1.0"
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
deployment:
  devkitId: mini-devkit
  baseline:
    id: baseline-v1
    url: ` + baselineURL + "\n")
	if baselineSigURL != "" {
		b.WriteString("    signed: true\n    signatureUrl: " + baselineSigURL + "\n    signingPublicKeyLookupUrl: " + keyURL + "\n")
	} else {
		b.WriteString("    signed: false\n")
	}
	if policyURL != "" {
		b.WriteString(`  policy:
    type: rego
    source: file
    file:
      id: deployment-policy
      url: ` + policyURL + `
      policyQueryPath: data.deployment.policy.result
      signed: false
`)
	}
	if collectorURL != "" {
		b.WriteString("observability:\n  enabled: true\n  collector:\n    url: " + collectorURL + "\n")
	}
	b.WriteString(`governance:
  effectiveFrom: "2020-01-01T00:00:00Z"
  signed: false
`)
	return []byte(b.String())
}

// TestResolveManifestLocalFile checks the development path: parse and
// validate a local manifest without signature verification.
func TestResolveManifestLocalFile(t *testing.T) {
	manifest, err := ResolveManifest(context.Background(), ManifestSource{
		LocalFile: testManifestYAML("https://example.org/baseline.yaml", "", "", "", ""),
		Timeout:   time.Second,
	})
	if err != nil {
		t.Fatalf("ResolveManifest: %v", err)
	}
	if manifest.Deployment == nil || manifest.Deployment.DevkitID != "mini-devkit" {
		t.Fatalf("deployment section not parsed: %+v", manifest.Deployment)
	}

	if _, err := ResolveManifest(context.Background(), ManifestSource{LocalFile: []byte(":not yaml:")}); err == nil {
		t.Fatalf("expected parse error")
	}
	if _, err := ResolveManifest(context.Background(), ManifestSource{
		LocalFile: []byte("manifestVersion: \"1.0\"\nmanifestType: other\n"),
	}); err == nil || !strings.Contains(err.Error(), "network-manifest") {
		t.Fatalf("expected validation error, got %v", err)
	}
}

// TestResolveManifestByMetadata checks the explicit-URL path with full
// detached-signature verification through the manifestloader plugin.
func TestResolveManifestByMetadata(t *testing.T) {
	priv, pubPEM := signingKeys(t)
	content := testManifestYAML("https://example.org/baseline.yaml", "", "", "", "")
	signature := ed25519.Sign(priv, content)

	server := artifactServer(t, map[string][]byte{
		"/manifest.yaml":     content,
		"/manifest.yaml.sig": signature,
		"/public-key":        pubPEM,
	})

	manifest, err := ResolveManifest(context.Background(), ManifestSource{
		Metadata: model.ManifestMetadata{
			ManifestURL:               server.URL + "/manifest.yaml",
			ManifestSignatureURL:      server.URL + "/manifest.yaml.sig",
			SigningPublicKeyLookupURL: server.URL + "/public-key",
		},
		Timeout: 5 * time.Second,
	})
	if err != nil {
		t.Fatalf("ResolveManifest: %v", err)
	}
	if manifest.NetworkID != "example.org/testnet" {
		t.Fatalf("networkId = %q", manifest.NetworkID)
	}

	// Wrong signature must fail.
	badServer := artifactServer(t, map[string][]byte{
		"/manifest.yaml":     content,
		"/manifest.yaml.sig": []byte("bogus"),
		"/public-key":        pubPEM,
	})
	_, err = ResolveManifest(context.Background(), ManifestSource{
		Metadata: model.ManifestMetadata{
			ManifestURL:               badServer.URL + "/manifest.yaml",
			ManifestSignatureURL:      badServer.URL + "/manifest.yaml.sig",
			SigningPublicKeyLookupURL: badServer.URL + "/public-key",
		},
		Timeout: 5 * time.Second,
	})
	if err == nil || !strings.Contains(err.Error(), "signature verification failed") {
		t.Fatalf("expected signature failure, got %v", err)
	}
}

// TestFetchVerifiedArtifact checks unsigned fetch, signed fetch, tampered
// content, and missing artifacts.
func TestFetchVerifiedArtifact(t *testing.T) {
	priv, pubPEM := signingKeys(t)
	content := []byte("baselineVersion: \"1.0\"\n")
	signature := ed25519.Sign(priv, content)
	server := artifactServer(t, map[string][]byte{
		"/baseline.yaml":     content,
		"/baseline.yaml.sig": signature,
		"/tampered.yaml":     append([]byte("x"), content...),
		"/public-key":        pubPEM,
	})
	client := &http.Client{Timeout: 5 * time.Second}
	ctx := context.Background()

	unsigned := &model.NetworkManifestArtifactRef{ID: "b", URL: server.URL + "/baseline.yaml"}
	if got, err := FetchVerifiedArtifact(ctx, client, unsigned, false); err != nil || string(got) != string(content) {
		t.Fatalf("unsigned fetch: %v", err)
	}

	signed := &model.NetworkManifestArtifactRef{
		ID:                        "b",
		URL:                       server.URL + "/baseline.yaml",
		Signed:                    true,
		SignatureURL:              server.URL + "/baseline.yaml.sig",
		SigningPublicKeyLookupURL: server.URL + "/public-key",
	}
	if _, err := FetchVerifiedArtifact(ctx, client, signed, false); err != nil {
		t.Fatalf("signed fetch: %v", err)
	}

	tampered := *signed
	tampered.URL = server.URL + "/tampered.yaml"
	if _, err := FetchVerifiedArtifact(ctx, client, &tampered, false); err == nil {
		t.Fatalf("tampered artifact must fail verification")
	}
	// skipVerify bypasses the check (development only).
	if _, err := FetchVerifiedArtifact(ctx, client, &tampered, true); err != nil {
		t.Fatalf("skipVerify fetch: %v", err)
	}

	missing := &model.NetworkManifestArtifactRef{ID: "m", URL: server.URL + "/nope"}
	if _, err := FetchVerifiedArtifact(ctx, client, missing, false); err == nil {
		t.Fatalf("missing artifact must fail")
	}
}

// TestFetchBoundedSizeCap checks that oversized responses are refused.
func TestFetchBoundedSizeCap(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(make([]byte, maxRemoteArtifactBytes+1))
	}))
	t.Cleanup(server.Close)
	_, err := fetchBounded(context.Background(), &http.Client{Timeout: 30 * time.Second}, server.URL)
	if err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("expected size-cap error, got %v", err)
	}
}

// TestNoopRegistry checks that the explicit-metadata path's registry stub
// always fails loudly.
func TestNoopRegistry(t *testing.T) {
	if _, err := (noopRegistry{}).LookupRegistry(context.Background(), "ns", "reg"); err == nil {
		t.Fatalf("noopRegistry must always error")
	}
}
