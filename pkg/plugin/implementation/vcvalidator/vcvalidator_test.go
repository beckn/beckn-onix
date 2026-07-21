package vcvalidator

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lestrrat-go/jwx/v3/jwa"
	"github.com/lestrrat-go/jwx/v3/jwk"
	"github.com/lestrrat-go/jwx/v3/jws"

	"github.com/beckn-one/beckn-onix/pkg/model"
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
		wantCode  string    // checked when wantClass is non-empty
	}{
		{"didkey-unrevoked.json", "", ""},
		{"didkey-revoked.json", failRevoked, codeAutKeyExpiredOrRevoked},
		{"didjwk-unrevoked.json", "", ""},
		{"didjwk-revoked.json", failRevoked, codeAutKeyExpiredOrRevoked},
		{"didweb-unrevoked.json", "", ""},
		{"didweb-revoked.json", failRevoked, codeAutKeyExpiredOrRevoked},
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
			assertClassCode(t, err, tc.wantClass, tc.wantCode)
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
	assertClassCode(t, err, failProof, codeAutSignatureInvalid)
}

func TestExpired(t *testing.T) {
	v := testVerifier(nil)
	v.now = func() time.Time { return time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC) }
	err := v.verify(context.Background(), vcBytes(t, loadVC(t)))
	assertClassCode(t, err, failExpired, codeAutKeyExpiredOrRevoked)
}

func TestNotYetValid(t *testing.T) {
	v := testVerifier(nil)
	v.now = func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) }
	err := v.verify(context.Background(), vcBytes(t, loadVC(t)))
	assertClassCode(t, err, failExpired, codeAutKeyExpiredOrRevoked)
}

func TestIssuerMismatch(t *testing.T) {
	vc := loadVC(t)
	// keep the JWT (signed by the real did:key) but claim a different issuer.
	vc["issuer"] = "did:key:z6MkpTHR8VNsBxYAAWHut2Geadd9jSwuBV8xRoAnwWsdvktH"
	v := testVerifier(nil)
	err := v.verify(context.Background(), vcBytes(t, vc))
	assertClassCode(t, err, failIssuer, codeAutUnauthorizedAction)
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
	// "dial tcp: no such host" is network-caused (isNetErr) but not a timeout,
	// so it lands on NET_DOWNSTREAM_UNAVAILABLE rather than NET_TIMEOUT.
	assertClassCode(t, err, failResolution, codeNetDownstreamUnavailable)

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
	assertClassCode(t, v.verify(context.Background(), raw), failRevoked, codeAutKeyExpiredOrRevoked)
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
	assertClassCode(t, err, failProof, codeAutSignatureInvalid)
}

// TestResolutionCode exercises resolutionCode's three-way split directly: a
// timeout is NET_TIMEOUT, another network cause (matched by the same isNetErr
// heuristic) is NET_DOWNSTREAM_UNAVAILABLE, and anything else — the key
// genuinely couldn't be found rather than an unreachable network — is
// AUT_KEY_NOT_FOUND.
// fakeTimeoutErr implements the timeouter interface (Timeout() bool) the way
// *url.Error/net.Error do — its message text deliberately contains no
// "timeout" substring, so a test built on it only passes if isTimeoutErr
// actually uses the interface rather than falling back to string matching.
type fakeTimeoutErr struct{ msg string }

func (e *fakeTimeoutErr) Error() string { return e.msg }
func (e *fakeTimeoutErr) Timeout() bool { return true }

func TestResolutionCode(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want string
	}{
		{"network timeout (string fallback)", fmt.Errorf("did:web fetch https://x: dial tcp: i/o timeout"), codeNetTimeout},
		{"network, not a timeout", fmt.Errorf("did:web fetch https://x: dial tcp: no such host"), codeNetDownstreamUnavailable},
		{"non-network", fmt.Errorf("did method %q not allowed (allowed: [key jwk web])", "example"), codeAutKeyNotFound},
		{
			// msg deliberately contains no "timeout" substring anywhere, so
			// this only passes if isTimeoutErr actually consults the
			// Timeout() interface rather than falling back to string
			// matching.
			"timeout detected via Timeout() interface, not message text",
			fmt.Errorf("did:web fetch https://x: %w", &fakeTimeoutErr{msg: "context deadline exceeded (no server response)"}),
			codeNetTimeout,
		},
		{
			"ordinary non-2xx HTTP status is not a network failure",
			fmt.Errorf("did:web fetch https://x: http 404 for https://x"),
			codeAutKeyNotFound,
		},
		{
			// A real *url.Error (what http.Client.Do returns) wrapping a TLS
			// failure — message text contains none of "dial"/"no such
			// host"/"connection"/"timeout", so this only passes if isNetErr's
			// errors.As(*url.Error) type check is actually working.
			"real *url.Error (TLS failure) is a network failure despite no recognizable substring",
			fmt.Errorf("did:web fetch https://x: %w", &url.Error{Op: "Get", URL: "https://x", Err: errors.New("tls: failed to verify certificate: x509: certificate signed by unknown authority")}),
			codeNetDownstreamUnavailable,
		},
		{
			"real *url.Error wrapping a genuine timeout",
			fmt.Errorf("did:web fetch https://x: %w", &url.Error{Op: "Get", URL: "https://x", Err: &fakeTimeoutErr{msg: "context deadline exceeded (no server response)"}}),
			codeNetTimeout,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := resolutionCode(tt.err); got != tt.want {
				t.Errorf("resolutionCode(%v) = %s, want %s", tt.err, got, tt.want)
			}
		})
	}
}

// TestNoProof asserts a credential with no proof block at all is distinguished
// from every other proof failure: AUT_SIGNATURE_MISSING, not
// AUT_SIGNATURE_INVALID.
func TestNoProof(t *testing.T) {
	vc := map[string]any{
		"issuer":            "did:key:z6MkpTHR8VNsBxYAAWHut2Geadd9jSwuBV8xRoAnwWsdvktH",
		"credentialSubject": map[string]any{"id": "x"},
	}
	v := testVerifier(nil)
	err := v.verify(context.Background(), vcBytes(t, vc))
	assertClassCode(t, err, failProof, codeAutSignatureMissing)
}

// TestProofPresentButEmpty asserts a credential whose proof object exists but
// has neither jwt nor proofValue is classified the same as no proof at all
// (AUT_SIGNATURE_MISSING) — both mean there's no usable proof content, so
// they shouldn't be split across two different codes.
func TestProofPresentButEmpty(t *testing.T) {
	vc := map[string]any{
		"issuer":            "did:key:z6MkpTHR8VNsBxYAAWHut2Geadd9jSwuBV8xRoAnwWsdvktH",
		"credentialSubject": map[string]any{"id": "x"},
		"proof":             map[string]any{"type": "SomeProofType"},
	}
	v := testVerifier(nil)
	err := v.verify(context.Background(), vcBytes(t, vc))
	assertClassCode(t, err, failProof, codeAutSignatureMissing)
}

// TestUnsupportedDIDMethodIsKeyNotFound asserts a resolution failure caused by
// a disallowed/unsupported DID method (not a network problem) is classified
// AUT_KEY_NOT_FOUND rather than a NET_* code.
func TestUnsupportedDIDMethodIsKeyNotFound(t *testing.T) {
	_, priv, _ := ed25519.GenerateKey(nil)
	did := "did:example:abc"
	kid := did + "#key-1"
	jwt := signVCJWT(t, priv, kid, did, "did:key:z6MkSubject", fixedNow())
	vc := map[string]any{
		"issuer":            did,
		"credentialSubject": map[string]any{"id": "did:key:z6MkSubject"},
		"proof":             map[string]any{"type": "JsonWebSignature2020", "jwt": jwt},
	}
	v := testVerifier(nil)
	v.now = fixedNow
	err := v.verify(context.Background(), vcBytes(t, vc))
	assertClassCode(t, err, failResolution, codeAutKeyNotFound)
}

// TestMalformedCredentialJSON asserts a credential body that isn't valid JSON
// at all is classified SCH_INVALID_JSON, distinct from a well-formed-but-
// incomplete credential (SCH_REQUIRED_FIELD_MISSING).
func TestMalformedCredentialJSON(t *testing.T) {
	v := testVerifier(nil)
	err := v.verify(context.Background(), json.RawMessage(`{"issuer": "x", "credentialSubject": {`))
	assertClassCode(t, err, failStructure, codeSchInvalidJSON)
}

// TestMalformedCredentialStatus asserts an unparseable credentialStatus is
// classified SCH_INVALID_JSON, same as any other malformed-JSON structural
// failure.
func TestMalformedCredentialStatus(t *testing.T) {
	vc := loadVC(t)
	vc["credentialStatus"] = 123 // neither an object nor an array
	v := testVerifier(nil)
	err := v.verify(context.Background(), vcBytes(t, vc))
	assertClassCode(t, err, failStructure, codeSchInvalidJSON)
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

func TestConfigMaxCredentialsAndPrivateNetworks(t *testing.T) {
	cfg, err := ParseConfig(map[string]string{
		"actions": "confirm", "maxCredentials": "3", "allowPrivateNetworks": "true",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.MaxCredentials != 3 {
		t.Fatalf("expected maxCredentials=3, got %d", cfg.MaxCredentials)
	}
	if !cfg.AllowPrivateNetworks {
		t.Fatal("expected allowPrivateNetworks=true")
	}

	if def, err := ParseConfig(map[string]string{"actions": "confirm"}); err != nil || def.MaxCredentials != 10 || def.AllowPrivateNetworks {
		t.Fatalf("expected defaults maxCredentials=10 allowPrivateNetworks=false, got %+v (%v)", def, err)
	}

	for _, bad := range []string{"0", "-1", "abc"} {
		if _, err := ParseConfig(map[string]string{"actions": "confirm", "maxCredentials": bad}); err == nil {
			t.Fatalf("expected error for maxCredentials=%q", bad)
		}
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

// ── step tests ──────────────────────────────────────────────────────────

// testStep builds the Step around an offline verifier gating "confirm".
func testStep() *step {
	cfg := DefaultConfig()
	cfg.Actions = []string{"confirm"}
	return &step{cfg: cfg, v: testVerifier(cfg)}
}

// stepCtx builds a StepContext for a POST of body to the given path.
func stepCtx(path string, body []byte) *model.StepContext {
	return &model.StepContext{
		Context: context.Background(),
		Request: httptest.NewRequest(http.MethodPost, path, bytes.NewReader(body)),
		Body:    body,
	}
}

// becknBody wraps a credential in a beckn envelope the way participants embed
// VCs (message.contract.participants[].participantAttributes).
func becknBody(t *testing.T, action string, vc map[string]any) []byte {
	t.Helper()
	body, err := json.Marshal(map[string]any{
		"context": map[string]any{"action": action, "messageId": "m-1"},
		"message": map[string]any{"contract": map[string]any{"participants": []any{
			map[string]any{"id": "p1", "participantAttributes": vc},
		}}},
	})
	if err != nil {
		t.Fatalf("marshal beckn body: %v", err)
	}
	return body
}

// TestStepPassThrough covers the cases where Run must not reject: plugin
// disabled, non-gated action, and a gated action without embedded credentials.
func TestStepPassThrough(t *testing.T) {
	badVC := loadVC(t)
	badVC["issuer"] = "did:key:z6MkpTHR8VNsBxYAAWHut2Geadd9jSwuBV8xRoAnwWsdvktH" // would fail if verified

	t.Run("disabled", func(t *testing.T) {
		s := testStep()
		s.cfg.Enabled = false
		if err := s.Run(stepCtx("/bpp/receiver/confirm", becknBody(t, "confirm", badVC))); err != nil {
			t.Fatalf("disabled step must pass through, got: %v", err)
		}
	})
	t.Run("non-gated action", func(t *testing.T) {
		if err := testStep().Run(stepCtx("/bpp/receiver/search", becknBody(t, "search", badVC))); err != nil {
			t.Fatalf("non-gated action must pass through, got: %v", err)
		}
	})
	t.Run("no credentials", func(t *testing.T) {
		body := []byte(`{"context":{"action":"confirm"},"message":{"order":{}}}`)
		if err := testStep().Run(stepCtx("/bpp/receiver/confirm", body)); err != nil {
			t.Fatalf("body without credentials must pass through, got: %v", err)
		}
	})
	t.Run("valid credential", func(t *testing.T) {
		if err := testStep().Run(stepCtx("/bpp/receiver/confirm", becknBody(t, "confirm", loadVC(t)))); err != nil {
			t.Fatalf("valid credential must pass, got: %v", err)
		}
	})
}

// TestStepNackErrorTypes asserts that rejections carry the model error type
// the handler's NACK mapping understands: BadReqErr for structurally broken
// credentials, SignValidationErr for authenticity failures. The failure class
// must survive in the error message.
func TestStepNackErrorTypes(t *testing.T) {
	t.Run("authenticity failure is SignValidationErr", func(t *testing.T) {
		vc := loadVC(t)
		vc["issuer"] = "did:key:z6MkpTHR8VNsBxYAAWHut2Geadd9jSwuBV8xRoAnwWsdvktH" // signer ≠ issuer
		err := testStep().Run(stepCtx("/bpp/receiver/confirm", becknBody(t, "confirm", vc)))
		var signErr *model.SignValidationErr
		if !errors.As(err, &signErr) {
			t.Fatalf("expected *model.SignValidationErr, got %T: %v", err, err)
		}
		if !strings.Contains(err.Error(), string(failIssuer)) {
			t.Fatalf("failure class missing from error: %v", err)
		}
		if code := signErr.BecknError().Code; code != codeAutUnauthorizedAction {
			t.Fatalf("BecknError().Code = %s, want %s", code, codeAutUnauthorizedAction)
		}
	})
	t.Run("structural failure is BadReqErr", func(t *testing.T) {
		vc := map[string]any{ // no issuer → failStructure
			"credentialSubject": map[string]any{"id": "x"},
			"proof":             map[string]any{"jwt": "a.b.c"},
		}
		err := testStep().Run(stepCtx("/bpp/receiver/confirm", becknBody(t, "confirm", vc)))
		var badReq *model.BadReqErr
		if !errors.As(err, &badReq) {
			t.Fatalf("expected *model.BadReqErr, got %T: %v", err, err)
		}
		if !strings.Contains(err.Error(), string(failStructure)) {
			t.Fatalf("failure class missing from error: %v", err)
		}
		if code := badReq.BecknError().Code; code != codeSchRequiredFieldMissing {
			t.Fatalf("BecknError().Code = %s, want %s", code, codeSchRequiredFieldMissing)
		}
	})
}

// TestStepMaxCredentials asserts the per-request credential cap: a request
// carrying more than maxCredentials must be rejected as a Bad Request before
// any credential is verified — even if the individual credentials would fail
// verification for a different reason.
func TestStepMaxCredentials(t *testing.T) {
	badVC := loadVC(t)
	badVC["issuer"] = "did:key:z6MkpTHR8VNsBxYAAWHut2Geadd9jSwuBV8xRoAnwWsdvktH" // ISSUER_MISMATCH if verified

	s := testStep()
	s.cfg.MaxCredentials = 5

	participants := make([]any, 6)
	for i := range participants {
		participants[i] = map[string]any{"id": fmt.Sprintf("p%d", i), "participantAttributes": badVC}
	}
	body, err := json.Marshal(map[string]any{
		"context": map[string]any{"action": "confirm", "messageId": "m-1"},
		"message": map[string]any{"contract": map[string]any{"participants": participants}},
	})
	if err != nil {
		t.Fatalf("marshal body: %v", err)
	}

	runErr := s.Run(stepCtx("/bpp/receiver/confirm", body))
	var badReq *model.BadReqErr
	if !errors.As(runErr, &badReq) {
		t.Fatalf("expected *model.BadReqErr, got %T: %v", runErr, runErr)
	}
	if !strings.Contains(runErr.Error(), "maxCredentials") {
		t.Fatalf("expected cap rejection, got: %v", runErr)
	}
	if code := badReq.BecknError().Code; code != codeSchSchemaValidation {
		t.Fatalf("BecknError().Code = %s, want %s", code, codeSchSchemaValidation)
	}

	// At the cap it must fall through to per-credential verification.
	s.cfg.MaxCredentials = 6
	runErr = s.Run(stepCtx("/bpp/receiver/confirm", body))
	if runErr == nil || !strings.Contains(runErr.Error(), string(failIssuer)) {
		t.Fatalf("expected per-credential verification at the cap, got: %v", runErr)
	}
}

// ── outbound fetch hardening ────────────────────────────────────────────

func TestIsDisallowedIP(t *testing.T) {
	cases := []struct {
		ip   string
		want bool
	}{
		{"127.0.0.1", true},             // loopback
		{"10.1.2.3", true},              // RFC1918
		{"172.16.0.1", true},            // RFC1918
		{"192.168.1.1", true},           // RFC1918
		{"169.254.169.254", true},       // link-local / cloud metadata
		{"0.0.0.0", true},               // unspecified
		{"255.255.255.255", true},       // broadcast
		{"224.0.0.1", true},             // multicast
		{"::1", true},                   // v6 loopback
		{"fe80::1", true},               // v6 link-local
		{"fc00::1", true},               // v6 ULA
		{"::ffff:10.0.0.1", true},       // v4-mapped private
		{"8.8.8.8", false},              // public v4
		{"2001:4860:4860::8888", false}, // public v6
	}
	for _, tc := range cases {
		ip := net.ParseIP(tc.ip)
		if ip == nil {
			t.Fatalf("bad test ip %q", tc.ip)
		}
		if got := isDisallowedIP(ip); got != tc.want {
			t.Errorf("isDisallowedIP(%s) = %v, want %v", tc.ip, got, tc.want)
		}
	}
}

// TestGuardBlocksPrivateFetch asserts the default client refuses to dial a
// loopback destination (the SSRF guard), and that allowPrivateNetworks
// re-enables it for local deployments.
func TestGuardBlocksPrivateFetch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	cfg := DefaultConfig()
	cfg.Actions = []string{"confirm"}

	fetch := httpFetcher(newHTTPClient(cfg))
	if _, err := fetch(context.Background(), srv.URL); err == nil || !strings.Contains(err.Error(), "blocked dial") {
		t.Fatalf("expected blocked dial to loopback, got: %v", err)
	}

	cfg.AllowPrivateNetworks = true
	fetch = httpFetcher(newHTTPClient(cfg))
	if _, err := fetch(context.Background(), srv.URL); err != nil {
		t.Fatalf("allowPrivateNetworks: expected fetch to succeed, got: %v", err)
	}
}

// TestRedirectCap asserts redirect-following stops after maxRedirects hops.
func TestRedirectCap(t *testing.T) {
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, srv.URL+r.URL.Path+"x", http.StatusFound)
	}))
	defer srv.Close()

	cfg := DefaultConfig()
	cfg.Actions = []string{"confirm"}
	cfg.AllowPrivateNetworks = true // so the loopback test server is dialable

	fetch := httpFetcher(newHTTPClient(cfg))
	_, err := fetch(context.Background(), srv.URL)
	if err == nil || !strings.Contains(err.Error(), "stopped after 3 redirects") {
		t.Fatalf("expected redirect cap error, got: %v", err)
	}
}

// ── helpers ─────────────────────────────────────────────────────────────

// assertClassCode asserts err is a *vcError with the given class, and — when
// wantCode is non-empty — the given Beckn v2.0.0 ErrorCode too.
func assertClassCode(t *testing.T, err error, want failClass, wantCode string) {
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
	if wantCode != "" && ve.code != wantCode {
		t.Fatalf("expected code %s, got %s (%s)", wantCode, ve.code, ve.msg)
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
