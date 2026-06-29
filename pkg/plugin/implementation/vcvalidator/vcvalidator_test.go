package vcvalidator

import (
	"context"
	"crypto/ed25519"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lestrrat-go/jwx/v3/jwa"
	"github.com/lestrrat-go/jwx/v3/jwk"
	"github.com/lestrrat-go/jwx/v3/jws"
)

// fixedNow pins the clock to a moment inside every fixture's validity window
// (the generated vectors use 2026-01-01 .. 2027-12-31; the flockenergy VC uses
// 2026-06-04 .. 2026-12-04).
func fixedNow() time.Time { return time.Date(2026, 6, 27, 0, 0, 0, 0, time.UTC) }

func loadVC(t *testing.T) map[string]any {
	t.Helper()
	return readVCFile(t, "testdata/flockenergy_vc.json")
}

func readVCFile(t *testing.T, path string) map[string]any {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read fixture %s: %v", path, err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("parse fixture %s: %v", path, err)
	}
	return m
}

func vcBytes(t *testing.T, vc map[string]any) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(vc)
	if err != nil {
		t.Fatalf("marshal vc: %v", err)
	}
	return b
}

func testVerifier(cfg *Config) *verifier {
	if cfg == nil {
		cfg = DefaultConfig()
		cfg.Actions = []string{"confirm"}
	}
	v := newVerifier(cfg, func(ctx context.Context, url string) ([]byte, error) {
		return nil, fmt.Errorf("unexpected fetch: %s", url)
	})
	v.statusGet = func(ctx context.Context, url string) (int, []byte, error) {
		return 0, nil, fmt.Errorf("unexpected statusGet: %s", url)
	}
	v.now = fixedNow
	return v
}

// TestVectors exercises the committed mock credentials covering every supported
// DID method (did:key, did:jwk, did:web) in both a not-revoked and a revoked
// state. The DID document and the StatusList2021 credential they reference are
// served from testdata via an in-memory fetcher, so the test is fully offline.
// Regenerate the vectors with: go run testdata/gen/main.go
func TestVectors(t *testing.T) {
	fetch := vectorFetcher(t)

	cases := []struct {
		file      string
		wantClass failClass // "" => must be accepted
	}{
		{"didkey-unrevoked.json", ""},
		{"didkey-revoked.json", failRevoked},
		{"didjwk-unrevoked.json", ""},
		{"didjwk-revoked.json", failRevoked},
		{"didweb-unrevoked.json", ""},
		{"didweb-revoked.json", failRevoked},
	}

	for _, tc := range cases {
		t.Run(strings.TrimSuffix(tc.file, ".json"), func(t *testing.T) {
			cfg := DefaultConfig()
			cfg.Actions = []string{"confirm"}
			v := newVerifier(cfg, fetch)
			v.statusGet = func(ctx context.Context, url string) (int, []byte, error) {
				return 0, nil, fmt.Errorf("unexpected statusGet: %s", url)
			}
			v.now = fixedNow

			raw := vcBytes(t, readVCFile(t, filepath.Join("testdata", "vectors", tc.file)))
			err := v.verify(context.Background(), raw)
			if tc.wantClass == "" {
				if err != nil {
					t.Fatalf("expected %s to be accepted, got: %v", tc.file, err)
				}
				return
			}
			assertClass(t, err, tc.wantClass)
		})
	}
}

// vectorFetcher serves the did:web document and the status list credential that
// the generated vectors reference, mapping their URLs to the committed files.
func vectorFetcher(t *testing.T) fetcher {
	t.Helper()
	const (
		didWebDocURL  = "https://issuer.example.org/.well-known/did.json"
		statusListURL = "https://status.example.org/revocation/1"
	)
	docs := map[string]string{
		didWebDocURL:  "testdata/vectors/didweb-did.json",
		statusListURL: "testdata/vectors/statuslist.json",
	}
	return func(ctx context.Context, url string) ([]byte, error) {
		path, ok := docs[url]
		if !ok {
			return nil, fmt.Errorf("unexpected fetch: %s", url)
		}
		return os.ReadFile(path)
	}
}

// TestRealDIDKeyVC verifies the genuine flockenergy did:key (P-256) VC-JWT.
func TestRealDIDKeyVC(t *testing.T) {
	v := testVerifier(nil)
	if err := v.verify(context.Background(), vcBytes(t, loadVC(t))); err != nil {
		t.Fatalf("expected valid VC to pass, got: %v", err)
	}
}

func TestTamperedSignature(t *testing.T) {
	vc := loadVC(t)
	proof := vc["proof"].(map[string]any)
	jwt := proof["jwt"].(string)
	// flip the last char of the signature segment.
	last := jwt[len(jwt)-1]
	repl := byte('A')
	if last == 'A' {
		repl = 'B'
	}
	proof["jwt"] = jwt[:len(jwt)-1] + string(repl)

	v := testVerifier(nil)
	err := v.verify(context.Background(), vcBytes(t, vc))
	assertClass(t, err, failProof)
}

func TestExpired(t *testing.T) {
	v := testVerifier(nil)
	v.now = func() time.Time { return time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC) }
	err := v.verify(context.Background(), vcBytes(t, loadVC(t)))
	assertClass(t, err, failExpired)
}

func TestNotYetValid(t *testing.T) {
	v := testVerifier(nil)
	v.now = func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) }
	err := v.verify(context.Background(), vcBytes(t, loadVC(t)))
	assertClass(t, err, failExpired)
}

func TestIssuerMismatch(t *testing.T) {
	vc := loadVC(t)
	// keep the JWT (signed by the real did:key) but claim a different issuer.
	vc["issuer"] = "did:key:z6MkpTHR8VNsBxYAAWHut2Geadd9jSwuBV8xRoAnwWsdvktH"
	v := testVerifier(nil)
	err := v.verify(context.Background(), vcBytes(t, vc))
	assertClass(t, err, failIssuer)
}

// TestDIDWebHappyPath builds a VC-JWT signed by an ed25519 key, publishes the
// matching did:web document through an injected fetcher, and asserts the proof
// verifies.
func TestDIDWebHappyPath(t *testing.T) {
	did := "did:web:issuer.example.org"
	kid := did + "#key-1"
	pub, priv, _ := ed25519.GenerateKey(nil)
	didDoc := makeDIDWebDoc(t, did, kid, pub)

	jwt := signVCJWT(t, priv, kid, did, "did:key:z6MkSubject", fixedNow())
	vc := map[string]any{
		"issuer":            map[string]any{"id": did, "name": "Example Issuer"},
		"validFrom":         "2026-06-04T00:00:00Z",
		"validUntil":        "2026-12-04T23:59:59Z",
		"credentialSubject": map[string]any{"id": "did:key:z6MkSubject"},
		"proof":             map[string]any{"type": "JsonWebSignature2020", "jwt": jwt},
	}

	cfg := DefaultConfig()
	cfg.Actions = []string{"confirm"}
	v := newVerifier(cfg, func(ctx context.Context, url string) ([]byte, error) {
		if url == "https://issuer.example.org/.well-known/did.json" {
			return didDoc, nil
		}
		return nil, fmt.Errorf("unexpected fetch: %s", url)
	})
	v.now = fixedNow
	if err := v.verify(context.Background(), vcBytes(t, vc)); err != nil {
		t.Fatalf("expected did:web VC to pass, got: %v", err)
	}
}

func TestDIDWebUnreachableFailsClosed(t *testing.T) {
	did := "did:web:offline.example.org"
	kid := did + "#key-1"
	_, priv, _ := ed25519.GenerateKey(nil)
	jwt := signVCJWT(t, priv, kid, did, "did:key:z6MkSubject", fixedNow())
	vc := map[string]any{
		"issuer":            did,
		"credentialSubject": map[string]any{"id": "x"},
		"proof":             map[string]any{"type": "JsonWebSignature2020", "jwt": jwt},
	}
	cfg := DefaultConfig()
	cfg.Actions = []string{"confirm"}
	v := newVerifier(cfg, func(ctx context.Context, url string) ([]byte, error) {
		return nil, fmt.Errorf("dial tcp: no such host")
	})
	v.now = fixedNow
	err := v.verify(context.Background(), vcBytes(t, vc))
	assertClass(t, err, failResolution)

	// fail-open should let it through.
	cfg.FailOpen = true
	if err := v.verify(context.Background(), vcBytes(t, vc)); err != nil {
		t.Fatalf("fail-open: expected pass, got %v", err)
	}
}

// dediVC builds a did:web VC carrying a DEDI credentialStatus, plus the
// did:web doc and a fetch/statusGet wired verifier. statusCode is what the DEDI
// per-record lookup returns (200 = revoked record exists, 404 = not revoked).
func dediVC(t *testing.T, statusCode int) (*verifier, json.RawMessage) {
	t.Helper()
	did := "did:web:issuer.example.org"
	kid := did + "#key-1"
	pub, priv, _ := ed25519.GenerateKey(nil)
	didDoc := makeDIDWebDoc(t, did, kid, pub)
	jwt := signVCJWT(t, priv, kid, did, "x", fixedNow())
	statusID := "https://api.dedi.global/dedi/lookup/did:web:did.cord.network:NS/vc-revocation-registry/abc123"
	vc := map[string]any{
		"issuer":            did,
		"credentialSubject": map[string]any{"id": "x"},
		"credentialStatus": map[string]any{
			"id":            statusID,
			"type":          "dedi",
			"statusPurpose": "revocation",
		},
		"proof": map[string]any{"type": "JsonWebSignature2020", "jwt": jwt},
	}
	cfg := DefaultConfig()
	cfg.Actions = []string{"confirm"}
	v := newVerifier(cfg, func(ctx context.Context, url string) ([]byte, error) {
		if url == "https://issuer.example.org/.well-known/did.json" {
			return didDoc, nil
		}
		return nil, fmt.Errorf("unexpected fetch: %s", url)
	})
	v.statusGet = func(ctx context.Context, url string) (int, []byte, error) {
		if url == statusID {
			return statusCode, []byte(`{"message":"..."}`), nil
		}
		return 0, nil, fmt.Errorf("unexpected statusGet: %s", url)
	}
	v.now = fixedNow
	return v, vcBytes(t, vc)
}

func TestDEDIRevoked(t *testing.T) {
	v, raw := dediVC(t, 200) // DEDI record exists → revoked
	assertClass(t, v.verify(context.Background(), raw), failRevoked)
}

func TestDEDINotRevoked(t *testing.T) {
	v, raw := dediVC(t, 404) // no DEDI record → not revoked → passes
	if err := v.verify(context.Background(), raw); err != nil {
		t.Fatalf("expected not-revoked DEDI VC to pass, got: %v", err)
	}
}

func TestDataIntegrityProofRejectedWhenRequired(t *testing.T) {
	vc := map[string]any{
		"issuer":            "did:web:issuer.example.org",
		"credentialSubject": map[string]any{"id": "x"},
		"proof": map[string]any{
			"type":               "Ed25519Signature2020",
			"proofValue":         "z58DAdFfa9Skq...",
			"verificationMethod": "did:web:issuer.example.org#key-1",
		},
	}
	v := testVerifier(nil)
	err := v.verify(context.Background(), vcBytes(t, vc))
	assertClass(t, err, failProof)
}

func TestConfigRequiresActions(t *testing.T) {
	_, err := ParseConfig(map[string]string{"enabled": "true"})
	if err == nil {
		t.Fatal("expected error when enabled with no actions")
	}
	if !strings.Contains(err.Error(), "actions is required") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestExtractCredentialsFromBecknBody(t *testing.T) {
	body := []byte(`{"context":{"action":"confirm"},"message":{"contract":{"participants":[
		{"id":"a"},
		{"id":"b","participantAttributes":{"proof":{"jwt":"x"},"credentialSubject":{"id":"y"},"issuer":"did:key:z"}}
	]}}}`)
	creds := extractCredentials(body)
	if len(creds) != 1 {
		t.Fatalf("expected 1 credential, got %d", len(creds))
	}
}

// ── helpers ─────────────────────────────────────────────────────────────

func assertClass(t *testing.T, err error, want failClass) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected error of class %s, got nil", want)
	}
	ve, ok := err.(*vcError)
	if !ok {
		t.Fatalf("expected *vcError, got %T: %v", err, err)
	}
	if ve.class != want {
		t.Fatalf("expected class %s, got %s (%s)", want, ve.class, ve.msg)
	}
}

func makeDIDWebDoc(t *testing.T, did, kid string, pub ed25519.PublicKey) []byte {
	t.Helper()
	key, err := jwk.Import(pub)
	if err != nil {
		t.Fatalf("import jwk: %v", err)
	}
	jwkJSON, err := json.Marshal(key)
	if err != nil {
		t.Fatalf("marshal jwk: %v", err)
	}
	doc := fmt.Sprintf(`{"id":%q,"verificationMethod":[{"id":%q,"type":"JsonWebKey2020","controller":%q,"publicKeyJwk":%s}]}`,
		did, kid, did, string(jwkJSON))
	return []byte(doc)
}

func signVCJWT(t *testing.T, priv ed25519.PrivateKey, kid, iss, sub string, now time.Time) string {
	t.Helper()
	claims := map[string]any{
		"iss": iss,
		"sub": sub,
		"nbf": now.Add(-time.Hour).Unix(),
		"exp": now.Add(24 * time.Hour).Unix(),
		"iat": now.Unix(),
	}
	payload, _ := json.Marshal(claims)
	hdr := jws.NewHeaders()
	_ = hdr.Set(jws.KeyIDKey, kid)
	signed, err := jws.Sign(payload, jws.WithKey(jwa.EdDSA(), priv, jws.WithProtectedHeaders(hdr)))
	if err != nil {
		t.Fatalf("sign jwt: %v", err)
	}
	return string(signed)
}
