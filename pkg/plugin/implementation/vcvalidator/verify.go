package vcvalidator

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/lestrrat-go/jwx/v3/jws"
)

// failClass identifies why a credential was rejected. It is surfaced in the
// NACK error code so the caller can tell apart e.g. an expired credential
// from a forged signature.
type failClass string

const (
	failStructure  failClass = "INVALID_CREDENTIAL"
	failProof      failClass = "INVALID_PROOF"
	failExpired    failClass = "CREDENTIAL_EXPIRED"
	failRevoked    failClass = "CREDENTIAL_REVOKED"
	failResolution failClass = "DID_RESOLUTION_FAILED"
	failIssuer     failClass = "ISSUER_MISMATCH"
)

// vcError is a credential validation failure with a machine-readable class.
type vcError struct {
	class failClass
	msg   string
}

func (e *vcError) Error() string { return string(e.class) + ": " + e.msg }

func failf(class failClass, format string, a ...any) *vcError {
	return &vcError{class: class, msg: fmt.Sprintf(format, a...)}
}

// credential is the subset of a W3C VC we inspect.
type credential struct {
	ID               string          `json:"id"`
	Type             json.RawMessage `json:"type"`
	Issuer           json.RawMessage `json:"issuer"`
	ValidFrom        string          `json:"validFrom"`
	ValidUntil       string          `json:"validUntil"`
	CredentialStatus json.RawMessage `json:"credentialStatus"`
	Proof            *proof          `json:"proof"`
}

type proof struct {
	Type               string `json:"type"`
	JWT                string `json:"jwt"`
	ProofValue         string `json:"proofValue"`
	VerificationMethod string `json:"verificationMethod"`
	Created            string `json:"created"`
}

// issuerDID extracts the issuer DID, whether issuer is a bare string or an
// object with an "id" field.
func (c *credential) issuerDID() (string, error) {
	if len(c.Issuer) == 0 {
		return "", failf(failStructure, "credential has no issuer")
	}
	var s string
	if err := json.Unmarshal(c.Issuer, &s); err == nil && s != "" {
		return s, nil
	}
	var obj struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(c.Issuer, &obj); err == nil && obj.ID != "" {
		return obj.ID, nil
	}
	return "", failf(failStructure, "credential issuer has no id")
}

// verifier validates credentials against the configured policy.
type verifier struct {
	cfg       *Config
	fetch     fetcher
	statusGet statusFetcher
	now       func() time.Time
}

func newVerifier(cfg *Config, fetch fetcher) *verifier {
	return &verifier{
		cfg:       cfg,
		fetch:     fetch,
		statusGet: httpStatusFetcher(http.DefaultClient),
		now:       time.Now,
	}
}

// verify runs all configured checks on a single credential. It returns a
// *vcError on rejection, or nil if the credential is acceptable.
func (v *verifier) verify(ctx context.Context, raw json.RawMessage) error {
	var cred credential
	if err := json.Unmarshal(raw, &cred); err != nil {
		return failf(failStructure, "cannot parse credential: %v", err)
	}
	issuer, err := cred.issuerDID()
	if err != nil {
		return err
	}

	// 1. Validity window (outer credential).
	if v.cfg.CheckExpiry {
		if err := v.checkWindow(cred.ValidFrom, cred.ValidUntil); err != nil {
			return err
		}
	}

	// 2. Proof.
	if cred.Proof == nil {
		return failf(failProof, "credential has no proof")
	}
	if cred.Proof.JWT != "" {
		if err := v.verifyJWTProof(ctx, &cred, issuer); err != nil {
			return err
		}
	} else if cred.Proof.ProofValue != "" {
		// JSON-LD Data Integrity proof (e.g. Ed25519Signature2020). Verifying
		// it requires RDF canonicalisation (URDNA2015), which this plugin does
		// not implement.
		if v.cfg.RequireProof {
			return failf(failProof,
				"proof type %q requires JSON-LD canonicalisation which is not supported; "+
					"set requireProof=false to accept on expiry/revocation only",
				cred.Proof.Type)
		}
		// Best-effort: confirm the verification method DID resolves.
		if vm := cred.Proof.VerificationMethod; vm != "" {
			if _, err := resolveDID(ctx, vm, "", v.cfg, v.fetch); err != nil {
				if !v.cfg.FailOpen {
					return failf(failResolution, "verificationMethod %q did not resolve: %v", vm, err)
				}
			}
		}
	} else {
		return failf(failProof, "proof has neither jwt nor proofValue")
	}

	// 3. Revocation.
	if v.cfg.CheckRevocation && len(cred.CredentialStatus) > 0 {
		if err := v.checkRevocation(ctx, cred.CredentialStatus); err != nil {
			return err
		}
	}

	return nil
}

// verifyJWTProof verifies a VC-JWT (proof.jwt) signature against the issuer's
// resolved DID key and enforces that the signer is the issuer.
func (v *verifier) verifyJWTProof(ctx context.Context, cred *credential, issuer string) error {
	token := cred.Proof.JWT
	header, err := decodeJWTHeader(token)
	if err != nil {
		return failf(failProof, "%v", err)
	}

	// The signing key DID comes from the JWT `kid` (its controller). It MUST
	// be the credential issuer — a credential signed by anyone other than its
	// issuer is rejected.
	signerDID := didOfKID(header.Kid)
	if signerDID == "" {
		signerDID = issuer
	}
	if base(signerDID) != base(issuer) {
		return failf(failIssuer,
			"proof signer %q does not match issuer %q", signerDID, issuer)
	}

	key, err := resolveDID(ctx, header.Kid, header.Alg, v.cfg, v.fetch)
	if err != nil {
		if isNetErr(err) && v.cfg.FailOpen {
			return nil
		}
		return failf(failResolution, "resolve %q: %v", header.Kid, err)
	}

	// Alg-confusion protection: the header alg must match the resolved key.
	if header.Alg != key.alg.String() {
		return failf(failProof,
			"header alg %q does not match issuer key algorithm %q", header.Alg, key.alg.String())
	}

	payload, err := jws.Verify([]byte(token), jws.WithKey(key.alg, key.pub))
	if err != nil {
		return failf(failProof, "signature verification failed: %v", err)
	}

	// Validate JWT temporal claims (nbf/exp) too.
	if v.cfg.CheckExpiry {
		if err := v.checkJWTClaims(payload); err != nil {
			return err
		}
	}
	return nil
}

type jwtHeader struct {
	Alg string `json:"alg"`
	Kid string `json:"kid"`
	Typ string `json:"typ"`
}

func decodeJWTHeader(token string) (*jwtHeader, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return nil, fmt.Errorf("malformed JWT: expected 3 segments, got %d", len(parts))
	}
	b, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return nil, fmt.Errorf("decode JWT header: %w", err)
	}
	var h jwtHeader
	if err := json.Unmarshal(b, &h); err != nil {
		return nil, fmt.Errorf("parse JWT header: %w", err)
	}
	if h.Alg == "" {
		return nil, fmt.Errorf("JWT header missing alg")
	}
	return &h, nil
}

func (v *verifier) checkJWTClaims(payload []byte) error {
	var claims struct {
		Nbf int64 `json:"nbf"`
		Exp int64 `json:"exp"`
	}
	if err := json.Unmarshal(payload, &claims); err != nil {
		return nil // no temporal claims to check
	}
	now := v.now().Unix()
	if claims.Nbf != 0 && now < claims.Nbf {
		return failf(failExpired, "credential not yet valid (nbf=%d, now=%d)", claims.Nbf, now)
	}
	if claims.Exp != 0 && now > claims.Exp {
		return failf(failExpired, "credential expired (exp=%d, now=%d)", claims.Exp, now)
	}
	return nil
}

// checkWindow enforces validFrom <= now <= validUntil (RFC3339).
func (v *verifier) checkWindow(validFrom, validUntil string) error {
	now := v.now()
	if validFrom != "" {
		t, err := time.Parse(time.RFC3339, validFrom)
		if err == nil && now.Before(t) {
			return failf(failExpired, "credential not yet valid (validFrom=%s)", validFrom)
		}
	}
	if validUntil != "" {
		t, err := time.Parse(time.RFC3339, validUntil)
		if err == nil && now.After(t) {
			return failf(failExpired, "credential expired (validUntil=%s)", validUntil)
		}
	}
	return nil
}

// didOfKID strips the #fragment from a kid that is itself a DID URL.
func didOfKID(kid string) string {
	if kid == "" {
		return ""
	}
	if !strings.HasPrefix(kid, "did:") {
		return ""
	}
	return base(kid)
}

// base returns the DID without any #fragment.
func base(did string) string {
	if i := strings.IndexByte(did, '#'); i >= 0 {
		return did[:i]
	}
	return did
}

func isNetErr(err error) bool {
	if err == nil {
		return false
	}
	s := err.Error()
	return strings.Contains(s, "fetch") || strings.Contains(s, "http ") ||
		strings.Contains(s, "dial") || strings.Contains(s, "timeout") ||
		strings.Contains(s, "no such host") || strings.Contains(s, "connection")
}
