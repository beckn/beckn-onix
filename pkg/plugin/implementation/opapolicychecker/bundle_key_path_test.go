package opapolicychecker

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// firstPathComponent returns the length of s up to its first "/": what the
// operating system sees as the first file name if s is taken for a path. A
// lookup fails on that name before it looks at anything after it.
func firstPathComponent(s string) int {
	if i := strings.Index(s, "/"); i >= 0 {
		return i
	}
	return len(s)
}

// The bundle verification key is PEM text. It must never be looked up as a
// file path: base64 contains "/", and a key whose text happens to have more
// than 255 characters before its first slash makes stat fail with "file name
// too long" instead of "not found", which used to fail the bundle load.
// Whether a deployment could load its signed policy bundle therefore depended
// on the bytes of its key (roughly one RSA-2048 key in forty).
func TestBundleVerification_KeyTextIsNeverTreatedAsAPath(t *testing.T) {
	policy := "package retail.validation\n\nimport rego.v1\n\ndefault result := {\"valid\": true, \"violations\": []}\n"

	var bundleData []byte
	var publicPEM string
	found := false
	for i := 0; i < 400 && !found; i++ {
		bundleData, publicPEM = buildSignedTestBundle(t, map[string]string{"retail/validation.rego": policy})
		found = firstPathComponent(publicPEM) > 255
	}
	if !found {
		t.Skip("no key whose text starts with 255+ slash-free characters in 400 attempts")
	}

	dir := t.TempDir()
	bundlePath := filepath.Join(dir, "signed-bundle.tar.gz")
	publicKeyPath := filepath.Join(dir, "bundle-public.pem")
	if err := os.WriteFile(bundlePath, bundleData, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(publicKeyPath, []byte(publicPEM), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := NewEvaluator([]string{bundlePath}, "data.retail.validation.result", nil, true, 0,
		&ArtifactVerificationConfig{Enabled: true, PublicKeyLookupURL: publicKeyPath, Algorithm: "RS256"})
	if err != nil {
		t.Fatalf("a correctly signed bundle was rejected because of the shape of its key text: %v", err)
	}
}
