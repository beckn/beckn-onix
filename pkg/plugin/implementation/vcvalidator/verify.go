// verify.go is the verification engine behind the vcvalidator Step: proof and
// validity-window checks, DID resolution (did:key / did:jwk / did:web), and
// revocation (StatusList2021 / BitstringStatusList, DEDI, generic).
package vcvalidator

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/decred/dcrd/dcrec/secp256k1/v4"
	"github.com/lestrrat-go/jwx/v3/jwa"
	"github.com/lestrrat-go/jwx/v3/jwk"
	"github.com/lestrrat-go/jwx/v3/jws"
)

// ---------------------------------------------------------------------------
// Failure classes
// ---------------------------------------------------------------------------

// failClass identifies why a credential was rejected. It is surfaced at the
// start of the NACK error message so the caller can tell apart e.g. an
// expired credential from a forged signature.
type failClass string

const (
	failStructure  failClass = "INVALID_CREDENTIAL"
	failProof      failClass = "INVALID_PROOF"
	failExpired    failClass = "CREDENTIAL_EXPIRED"
	failRevoked    failClass = "CREDENTIAL_REVOKED"
	failResolution failClass = "DID_RESOLUTION_FAILED"
	failIssuer     failClass = "ISSUER_MISMATCH"
)

// Beckn v2.0.0 ErrorCode values this package classifies its failures onto.
// Named locally (rather than inlined at each of the ~20 call sites below,
// unlike the single-use-per-plugin convention elsewhere in this codebase)
// since several codes here are reused across many sites and a typo in a
// string literal wouldn't be caught by the compiler.
const (
	codeSchInvalidJSON          = "SCH_INVALID_JSON"
	codeSchRequiredFieldMissing = "SCH_REQUIRED_FIELD_MISSING"
	codeSchSchemaValidation     = "SCH_SCHEMA_VALIDATION_FAILED"
	codeAutSignatureMissing     = "AUT_SIGNATURE_MISSING"
	codeAutSignatureInvalid     = "AUT_SIGNATURE_INVALID"
	codeAutUnauthorizedAction   = "AUT_UNAUTHORIZED_ACTION"
	// No dedicated credential-expiry/revocation code exists in the v2.0.0
	// taxonomy (CTX_TTL_EXPIRED is the beckn message TTL, a different
	// concept) — this is the closest existing bucket, reusing the pattern
	// keymanager/simplekeymanager already established for key lifecycle
	// expiry. Flagged in the PR description as a taxonomy-completeness gap.
	codeAutKeyExpiredOrRevoked = "AUT_KEY_EXPIRED_OR_REVOKED"
	codeAutKeyNotFound         = "AUT_KEY_NOT_FOUND"
	codeNetTimeout             = "NET_TIMEOUT"
	codeNetDownstreamUnavailable = "NET_DOWNSTREAM_UNAVAILABLE"
)

// vcError is a credential validation failure with a machine-readable class
// and the Beckn v2.0.0 ErrorCode nackErr carries to the wire.
type vcError struct {
	class failClass
	code  string
	msg   string
}

func (e *vcError) Error() string { return string(e.class) + ": " + e.msg }

func failf(class failClass, code, format string, a ...any) *vcError {
	return &vcError{class: class, code: code, msg: fmt.Sprintf(format, a...)}
}

// resolutionCode picks the ErrorCode for a failResolution failure: a
// network-caused resolution failure (detected via the same isNetErr
// heuristic already used for FailOpen decisions) gets a NET_* code, anything
// else (unsupported/disallowed DID method, malformed key material, no
// matching verification method) means the key genuinely couldn't be found.
func resolutionCode(err error) string {
	if !isNetErr(err) {
		return codeAutKeyNotFound
	}
	if strings.Contains(err.Error(), "timeout") {
		return codeNetTimeout
	}
	return codeNetDownstreamUnavailable
}

// ---------------------------------------------------------------------------
// Credential model
// ---------------------------------------------------------------------------

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
		return "", failf(failStructure, codeSchRequiredFieldMissing, "credential has no issuer")
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
	return "", failf(failStructure, codeSchRequiredFieldMissing, "credential issuer has no id")
}

// ---------------------------------------------------------------------------
// Verifier
// ---------------------------------------------------------------------------

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
		return failf(failStructure, codeSchInvalidJSON, "cannot parse credential: %v", err)
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
		return failf(failProof, codeAutSignatureMissing, "credential has no proof")
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
			return failf(failProof, codeAutSignatureInvalid,
				"proof type %q requires JSON-LD canonicalisation which is not supported; "+
					"set requireProof=false to accept on expiry/revocation only",
				cred.Proof.Type)
		}
		// Best-effort: confirm the verification method DID resolves.
		if vm := cred.Proof.VerificationMethod; vm != "" {
			if _, err := resolveDID(ctx, vm, "", v.cfg, v.fetch); err != nil {
				if !v.cfg.FailOpen {
					return failf(failResolution, resolutionCode(err), "verificationMethod %q did not resolve: %v", vm, err)
				}
			}
		}
	} else {
		return failf(failProof, codeAutSignatureInvalid, "proof has neither jwt nor proofValue")
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
		return failf(failProof, codeAutSignatureInvalid, "%v", err)
	}

	// The signing key DID comes from the JWT `kid` (its controller). It MUST
	// be the credential issuer — a credential signed by anyone other than its
	// issuer is rejected.
	signerDID := didOfKID(header.Kid)
	if signerDID == "" {
		signerDID = issuer
	}
	if base(signerDID) != base(issuer) {
		return failf(failIssuer, codeAutUnauthorizedAction,
			"proof signer %q does not match issuer %q", signerDID, issuer)
	}

	key, err := resolveDID(ctx, header.Kid, header.Alg, v.cfg, v.fetch)
	if err != nil {
		if isNetErr(err) && v.cfg.FailOpen {
			return nil
		}
		return failf(failResolution, resolutionCode(err), "resolve %q: %v", header.Kid, err)
	}

	// Alg-confusion protection: the header alg must match the resolved key.
	if header.Alg != key.alg.String() {
		return failf(failProof, codeAutSignatureInvalid,
			"header alg %q does not match issuer key algorithm %q", header.Alg, key.alg.String())
	}

	payload, err := jws.Verify([]byte(token), jws.WithKey(key.alg, key.pub))
	if err != nil {
		return failf(failProof, codeAutSignatureInvalid, "signature verification failed: %v", err)
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
		return failf(failExpired, codeAutKeyExpiredOrRevoked, "credential not yet valid (nbf=%d, now=%d)", claims.Nbf, now)
	}
	if claims.Exp != 0 && now > claims.Exp {
		return failf(failExpired, codeAutKeyExpiredOrRevoked, "credential expired (exp=%d, now=%d)", claims.Exp, now)
	}
	return nil
}

// checkWindow enforces validFrom <= now <= validUntil (RFC3339).
func (v *verifier) checkWindow(validFrom, validUntil string) error {
	now := v.now()
	if validFrom != "" {
		t, err := time.Parse(time.RFC3339, validFrom)
		if err == nil && now.Before(t) {
			return failf(failExpired, codeAutKeyExpiredOrRevoked, "credential not yet valid (validFrom=%s)", validFrom)
		}
	}
	if validUntil != "" {
		t, err := time.Parse(time.RFC3339, validUntil)
		if err == nil && now.After(t) {
			return failf(failExpired, codeAutKeyExpiredOrRevoked, "credential expired (validUntil=%s)", validUntil)
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

// ---------------------------------------------------------------------------
// DID resolution
// ---------------------------------------------------------------------------

// resolvedKey is a public key resolved from a DID together with the JWS
// signature algorithm it is expected to be used with.
type resolvedKey struct {
	pub crypto.PublicKey
	alg jwa.SignatureAlgorithm
}

// Multicodec varint prefixes for the key types we support in did:key.
var (
	mcEd25519   = []byte{0xed, 0x01} // ed25519-pub
	mcP256      = []byte{0x80, 0x24} // p256-pub  (0x1200)
	mcSecp256k1 = []byte{0xe7, 0x01} // secp256k1-pub
)

// didMethod returns the method segment of a DID ("key", "web", "jwk", ...).
func didMethod(did string) string {
	parts := strings.SplitN(did, ":", 3)
	if len(parts) < 2 || parts[0] != "did" {
		return ""
	}
	return parts[1]
}

// resolveDID resolves a DID (optionally with a #fragment selecting a specific
// verification method) to a public key. The header alg, when known, is used
// as a fallback for did methods that do not pin an algorithm.
func resolveDID(ctx context.Context, did, headerAlg string, cfg *Config, fetch fetcher) (*resolvedKey, error) {
	method := didMethod(did)
	if method == "" {
		return nil, fmt.Errorf("not a DID: %q", did)
	}
	if !cfg.IsMethodAllowed(method) {
		return nil, fmt.Errorf("did method %q not allowed (allowed: %v)", method, cfg.AllowedDIDMethods)
	}
	switch method {
	case "key":
		return resolveDIDKey(did)
	case "jwk":
		return resolveDIDJWK(did)
	case "web":
		return resolveDIDWeb(ctx, did, cfg, fetch)
	default:
		return nil, fmt.Errorf("unsupported did method %q", method)
	}
}

// resolveDIDKey decodes a did:key into a public key.
func resolveDIDKey(did string) (*resolvedKey, error) {
	// did:key:<mb>[#<fragment>]
	id := did[len("did:key:"):]
	if i := strings.IndexByte(id, '#'); i >= 0 {
		id = id[:i]
	}
	if len(id) == 0 || id[0] != 'z' {
		return nil, fmt.Errorf("did:key must use base58btc multibase ('z'), got %q", did)
	}
	raw, err := base58Decode(id[1:])
	if err != nil {
		return nil, fmt.Errorf("did:key base58 decode: %w", err)
	}
	return keyFromMulticodec(raw)
}

// keyFromMulticodec interprets multicodec-prefixed public key bytes.
func keyFromMulticodec(raw []byte) (*resolvedKey, error) {
	switch {
	case bytes.HasPrefix(raw, mcEd25519):
		k := raw[len(mcEd25519):]
		if len(k) != ed25519.PublicKeySize {
			return nil, fmt.Errorf("ed25519 key wrong size: %d", len(k))
		}
		return &resolvedKey{pub: ed25519.PublicKey(k), alg: jwa.EdDSA()}, nil
	case bytes.HasPrefix(raw, mcP256):
		pub, err := decompressP256(raw[len(mcP256):])
		if err != nil {
			return nil, err
		}
		return &resolvedKey{pub: pub, alg: jwa.ES256()}, nil
	case bytes.HasPrefix(raw, mcSecp256k1):
		pk, err := secp256k1.ParsePubKey(raw[len(mcSecp256k1):])
		if err != nil {
			return nil, fmt.Errorf("secp256k1 parse: %w", err)
		}
		return &resolvedKey{pub: pk.ToECDSA(), alg: jwa.ES256K()}, nil
	default:
		return nil, fmt.Errorf("unsupported multicodec prefix %x", raw[:min(2, len(raw))])
	}
}

func decompressP256(b []byte) (*ecdsa.PublicKey, error) {
	curve := elliptic.P256()
	if len(b) == 33 { // compressed
		x, y := elliptic.UnmarshalCompressed(curve, b)
		if x == nil {
			return nil, fmt.Errorf("invalid compressed P-256 point")
		}
		return &ecdsa.PublicKey{Curve: curve, X: x, Y: y}, nil
	}
	if len(b) == 65 { // uncompressed
		x, y := elliptic.Unmarshal(curve, b)
		if x == nil {
			return nil, fmt.Errorf("invalid uncompressed P-256 point")
		}
		return &ecdsa.PublicKey{Curve: curve, X: x, Y: y}, nil
	}
	return nil, fmt.Errorf("unexpected P-256 key length %d", len(b))
}

// resolveDIDJWK decodes a did:jwk into a public key.
func resolveDIDJWK(did string) (*resolvedKey, error) {
	id := did[len("did:jwk:"):]
	if i := strings.IndexByte(id, '#'); i >= 0 {
		id = id[:i]
	}
	dec, err := base64.RawURLEncoding.DecodeString(id)
	if err != nil {
		// tolerate padded base64url
		dec, err = base64.URLEncoding.DecodeString(id)
		if err != nil {
			return nil, fmt.Errorf("did:jwk base64url decode: %w", err)
		}
	}
	return keyFromJWKBytes(dec)
}

// didDocument is the minimal subset of a DID document we read.
type didDocument struct {
	ID                 string              `json:"id"`
	VerificationMethod []verificationEntry `json:"verificationMethod"`
	AssertionMethod    json.RawMessage     `json:"assertionMethod"`
}

type verificationEntry struct {
	ID                 string          `json:"id"`
	Type               string          `json:"type"`
	Controller         string          `json:"controller"`
	PublicKeyJwk       json.RawMessage `json:"publicKeyJwk"`
	PublicKeyMultibase string          `json:"publicKeyMultibase"`
	PublicKeyBase58    string          `json:"publicKeyBase58"`
}

// resolveDIDWeb fetches the did:web document and extracts the verification
// method's public key. If the DID carries a #fragment, the matching
// verification method is selected; otherwise the first one is used.
func resolveDIDWeb(ctx context.Context, did string, cfg *Config, fetch fetcher) (*resolvedKey, error) {
	base := did
	fragment := ""
	if i := strings.IndexByte(did, '#'); i >= 0 {
		base = did[:i]
		fragment = did[i+1:]
	}
	url, err := didWebURL(base)
	if err != nil {
		return nil, err
	}
	body, err := fetch(ctx, url)
	if err != nil {
		return nil, fmt.Errorf("did:web fetch %s: %w", url, err)
	}
	var doc didDocument
	if err := json.Unmarshal(body, &doc); err != nil {
		return nil, fmt.Errorf("did:web parse %s: %w", url, err)
	}
	if len(doc.VerificationMethod) == 0 {
		return nil, fmt.Errorf("did:web %s has no verificationMethod", base)
	}
	vm := selectVM(doc.VerificationMethod, did, fragment)
	if vm == nil {
		return nil, fmt.Errorf("did:web %s: verification method %q not found", base, fragment)
	}
	return keyFromVerificationMethod(vm)
}

func selectVM(vms []verificationEntry, fullDID, fragment string) *verificationEntry {
	if fragment == "" {
		return &vms[0]
	}
	want := fullDID
	for i := range vms {
		id := vms[i].ID
		// match full id, or by trailing fragment.
		if id == want || strings.HasSuffix(id, "#"+fragment) {
			return &vms[i]
		}
	}
	return nil
}

func keyFromVerificationMethod(vm *verificationEntry) (*resolvedKey, error) {
	if len(vm.PublicKeyJwk) > 0 {
		return keyFromJWKBytes(vm.PublicKeyJwk)
	}
	if vm.PublicKeyMultibase != "" {
		mb := vm.PublicKeyMultibase
		if mb[0] != 'z' {
			return nil, fmt.Errorf("publicKeyMultibase must be base58btc ('z')")
		}
		raw, err := base58Decode(mb[1:])
		if err != nil {
			return nil, fmt.Errorf("publicKeyMultibase decode: %w", err)
		}
		return keyFromMulticodec(raw)
	}
	if vm.PublicKeyBase58 != "" {
		raw, err := base58Decode(vm.PublicKeyBase58)
		if err != nil {
			return nil, fmt.Errorf("publicKeyBase58 decode: %w", err)
		}
		// publicKeyBase58 is raw (no multicodec). Ed25519Signature2018 used
		// this form with 32-byte ed25519 keys.
		if len(raw) == ed25519.PublicKeySize {
			return &resolvedKey{pub: ed25519.PublicKey(raw), alg: jwa.EdDSA()}, nil
		}
		return nil, fmt.Errorf("unsupported publicKeyBase58 length %d", len(raw))
	}
	return nil, fmt.Errorf("verification method %q has no public key material", vm.ID)
}

// keyFromJWKBytes parses a JWK JSON object into a public key + algorithm.
func keyFromJWKBytes(b []byte) (*resolvedKey, error) {
	key, err := jwk.ParseKey(b)
	if err != nil {
		return nil, fmt.Errorf("jwk parse: %w", err)
	}
	var raw any
	if err := jwk.Export(key, &raw); err != nil {
		return nil, fmt.Errorf("jwk export: %w", err)
	}
	pub := publicOf(raw)
	alg, err := algForKey(b, pub)
	if err != nil {
		return nil, err
	}
	return &resolvedKey{pub: pub, alg: alg}, nil
}

// publicOf returns the public half of a possibly-private key.
func publicOf(raw any) crypto.PublicKey {
	switch k := raw.(type) {
	case ed25519.PrivateKey:
		return k.Public()
	case *ecdsa.PrivateKey:
		return &k.PublicKey
	case ed25519.PublicKey, *ecdsa.PublicKey:
		return k
	default:
		return raw
	}
}

// algForKey determines the JWS algorithm. It prefers an explicit "alg"/"crv"
// in the JWK, falling back to the concrete key type.
func algForKey(jwkBytes []byte, pub crypto.PublicKey) (jwa.SignatureAlgorithm, error) {
	var meta struct {
		Alg string `json:"alg"`
		Crv string `json:"crv"`
		Kty string `json:"kty"`
	}
	_ = json.Unmarshal(jwkBytes, &meta)
	switch meta.Alg {
	case "EdDSA":
		return jwa.EdDSA(), nil
	case "ES256":
		return jwa.ES256(), nil
	case "ES256K":
		return jwa.ES256K(), nil
	case "ES384":
		return jwa.ES384(), nil
	case "ES512":
		return jwa.ES512(), nil
	}
	switch meta.Crv {
	case "Ed25519":
		return jwa.EdDSA(), nil
	case "P-256":
		return jwa.ES256(), nil
	case "secp256k1", "P-256K":
		return jwa.ES256K(), nil
	case "P-384":
		return jwa.ES384(), nil
	case "P-521":
		return jwa.ES512(), nil
	}
	// fall back to key type.
	switch k := pub.(type) {
	case ed25519.PublicKey:
		return jwa.EdDSA(), nil
	case *ecdsa.PublicKey:
		switch k.Curve {
		case elliptic.P256():
			return jwa.ES256(), nil
		case elliptic.P384():
			return jwa.ES384(), nil
		case elliptic.P521():
			return jwa.ES512(), nil
		}
	}
	return jwa.SignatureAlgorithm{}, fmt.Errorf("cannot determine algorithm for key")
}

// didWebURL converts a did:web identifier into the did.json URL.
//
//	did:web:example.com            -> https://example.com/.well-known/did.json
//	did:web:example.com:user:alice -> https://example.com/user/alice/did.json
//	did:web:example.com%3A3000     -> https://example.com:3000/.well-known/did.json
func didWebURL(did string) (string, error) {
	rest := strings.TrimPrefix(did, "did:web:")
	if rest == "" || rest == did {
		return "", fmt.Errorf("invalid did:web: %q", did)
	}
	parts := strings.Split(rest, ":")
	for i := range parts {
		// percent-encoded characters (e.g. %3A for ":") are decoded per spec.
		parts[i] = strings.ReplaceAll(parts[i], "%3A", ":")
	}
	host := parts[0]
	if len(parts) == 1 {
		return "https://" + host + "/.well-known/did.json", nil
	}
	return "https://" + host + "/" + strings.Join(parts[1:], "/") + "/did.json", nil
}

// ---------------------------------------------------------------------------
// Revocation
// ---------------------------------------------------------------------------

// statusEntry is one credentialStatus descriptor.
type statusEntry struct {
	ID                   string `json:"id"`
	Type                 string `json:"type"`
	StatusPurpose        string `json:"statusPurpose"`
	StatusListIndex      any    `json:"statusListIndex"`
	StatusListCredential string `json:"statusListCredential"`
}

// checkRevocation inspects credentialStatus (object or array) and rejects the
// credential if any revocation entry reports it as revoked.
func (v *verifier) checkRevocation(ctx context.Context, raw json.RawMessage) error {
	entries, err := parseStatusEntries(raw)
	if err != nil {
		return failf(failStructure, codeSchInvalidJSON, "credentialStatus: %v", err)
	}
	for _, e := range entries {
		// Only revocation-purpose entries gate acceptance. Empty purpose is
		// treated as revocation for back-compat with older issuers.
		if e.StatusPurpose != "" && !strings.EqualFold(e.StatusPurpose, "revocation") {
			continue
		}
		revoked, err := v.entryRevoked(ctx, e)
		if err != nil {
			if v.cfg.FailOpen {
				continue
			}
			return failf(failResolution, resolutionCode(err), "revocation check: %v", err)
		}
		if revoked {
			return failf(failRevoked, codeAutKeyExpiredOrRevoked, "credential revoked via %s", e.statusURL())
		}
	}
	return nil
}

func (e statusEntry) statusURL() string {
	if e.StatusListCredential != "" {
		return e.StatusListCredential
	}
	return e.ID
}

func parseStatusEntries(raw json.RawMessage) ([]statusEntry, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return nil, nil
	}
	if trimmed[0] == '[' {
		var arr []statusEntry
		if err := json.Unmarshal(trimmed, &arr); err != nil {
			return nil, err
		}
		return arr, nil
	}
	var one statusEntry
	if err := json.Unmarshal(trimmed, &one); err != nil {
		return nil, err
	}
	return []statusEntry{one}, nil
}

// entryRevoked resolves a single status entry to a revoked/not-revoked verdict.
func (v *verifier) entryRevoked(ctx context.Context, e statusEntry) (bool, error) {
	switch {
	case isDEDI(e):
		return v.dediRevoked(ctx, e)
	case strings.Contains(strings.ToLower(e.Type), "statuslist") && e.StatusListCredential != "":
		return v.statusListRevoked(ctx, e)
	default:
		// Unknown mechanism: fetch the status URL and look for a generic
		// revoked indicator.
		return v.genericRevoked(ctx, e.statusURL())
	}
}

// statusListRevoked implements StatusList2021 / BitstringStatusList lookup:
// fetch the status list credential, gzip-inflate the base64url-encoded
// bitstring, and test the bit at statusListIndex.
func (v *verifier) statusListRevoked(ctx context.Context, e statusEntry) (bool, error) {
	idx, err := toInt(e.StatusListIndex)
	if err != nil {
		return false, fmt.Errorf("statusListIndex: %w", err)
	}
	body, err := v.fetch(ctx, e.StatusListCredential)
	if err != nil {
		return false, err
	}
	var slc struct {
		CredentialSubject struct {
			EncodedList string `json:"encodedList"`
		} `json:"credentialSubject"`
	}
	if err := json.Unmarshal(body, &slc); err != nil {
		return false, fmt.Errorf("parse status list: %w", err)
	}
	if slc.CredentialSubject.EncodedList == "" {
		return false, fmt.Errorf("status list has no encodedList")
	}
	bits, err := decodeBitstring(slc.CredentialSubject.EncodedList)
	if err != nil {
		return false, err
	}
	byteIdx := idx / 8
	bitIdx := uint(idx % 8)
	if byteIdx >= len(bits) {
		return false, nil // index outside list ⇒ not set ⇒ not revoked
	}
	return bits[byteIdx]&(0x80>>bitIdx) != 0, nil
}

// decodeBitstring base64url-decodes then gzip-inflates a status list bitstring.
func decodeBitstring(encoded string) ([]byte, error) {
	raw, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		raw, err = base64.StdEncoding.DecodeString(encoded)
		if err != nil {
			return nil, fmt.Errorf("decode bitstring: %w", err)
		}
	}
	zr, err := gzip.NewReader(bytes.NewReader(raw))
	if err != nil {
		// some issuers store the bitstring uncompressed.
		return raw, nil
	}
	defer zr.Close()
	out, err := io.ReadAll(zr)
	if err != nil {
		return nil, fmt.Errorf("inflate bitstring: %w", err)
	}
	return out, nil
}

// isDEDI reports whether a credentialStatus entry references a DEDI revocation
// registry — by type (OpenCred writes "dedi"; older issuers "dediregistry") or
// by a DEDI lookup URL.
func isDEDI(e statusEntry) bool {
	if strings.HasPrefix(strings.ToLower(e.Type), "dedi") {
		return true
	}
	return strings.Contains(strings.ToLower(e.ID), "/dedi/lookup/") ||
		strings.Contains(strings.ToLower(e.StatusListCredential), "/dedi/lookup/")
}

// dediRevoked checks a DEDI revocation registry. DEDI stores ONLY revoked
// records, so the per-credential lookup URL (which embeds the credential hash)
// returns 200 when revoked and 404 when not — record existence, not body
// content, is the signal.
func (v *verifier) dediRevoked(ctx context.Context, e statusEntry) (bool, error) {
	url := e.ID
	if url == "" {
		url = e.StatusListCredential
	}
	if url == "" {
		return false, nil
	}
	status, _, err := v.statusGet(ctx, url)
	if err != nil {
		return false, err // transport failure → caller applies failOpen policy
	}
	switch {
	case status == http.StatusNotFound || status == http.StatusGone:
		return false, nil // no revocation record → not revoked
	case status >= 200 && status < 300:
		return true, nil // record exists → revoked
	default:
		return false, fmt.Errorf("dedi lookup %s: http %d", url, status)
	}
}

// genericRevoked fetches a status document and looks for common revoked
// indicators ("revoked": true, "status": "revoked"/"suspended").
func (v *verifier) genericRevoked(ctx context.Context, url string) (bool, error) {
	if url == "" {
		return false, nil
	}
	body, err := v.fetch(ctx, url)
	if err != nil {
		return false, err
	}
	var doc map[string]any
	if err := json.Unmarshal(body, &doc); err != nil {
		// non-JSON body: scan text.
		s := strings.ToLower(string(body))
		return strings.Contains(s, "\"revoked\":true") || strings.Contains(s, "revoked"), nil
	}
	return docRevoked(doc), nil
}

func docRevoked(doc map[string]any) bool {
	for k, val := range doc {
		lk := strings.ToLower(k)
		switch v := val.(type) {
		case bool:
			if lk == "revoked" && v {
				return true
			}
		case string:
			lv := strings.ToLower(v)
			if (lk == "status" || lk == "statuspurpose") && (lv == "revoked" || lv == "suspended") {
				return true
			}
		case map[string]any:
			if docRevoked(v) {
				return true
			}
		}
	}
	return false
}

func toInt(v any) (int, error) {
	switch n := v.(type) {
	case float64:
		return int(n), nil
	case int:
		return n, nil
	case string:
		return strconv.Atoi(n)
	case nil:
		return 0, fmt.Errorf("missing")
	default:
		return 0, fmt.Errorf("unexpected type %T", v)
	}
}

// ---------------------------------------------------------------------------
// HTTP fetchers
// ---------------------------------------------------------------------------

// maxRedirects caps redirect-following on did:web and revocation fetches.
// The destination guard sits at the dial layer, so every redirect hop is
// re-validated against it automatically.
const maxRedirects = 3

// newHTTPClient builds the http.Client shared by did:web resolution and
// revocation fetches. The URLs those fetches target are taken from the
// request body (the credential's issuer DID and credentialStatus), so unless
// AllowPrivateNetworks is set the client refuses to dial private, loopback,
// link-local or otherwise non-public addresses. The check runs on the IP
// actually being dialed — after DNS resolution — which also defeats DNS
// rebinding.
func newHTTPClient(cfg *Config) *http.Client {
	client := &http.Client{
		Timeout: cfg.HTTPTimeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= maxRedirects {
				return fmt.Errorf("stopped after %d redirects", maxRedirects)
			}
			return nil
		},
	}
	if !cfg.AllowPrivateNetworks {
		dialer := &net.Dialer{Control: guardDial}
		client.Transport = &http.Transport{DialContext: dialer.DialContext}
	}
	return client
}

// guardDial rejects dials to non-public addresses. address is the resolved
// "ip:port" the socket is about to connect to.
func guardDial(network, address string, _ syscall.RawConn) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return fmt.Errorf("blocked dial to %q: %v", address, err)
	}
	ip := net.ParseIP(host)
	if ip == nil || isDisallowedIP(ip) {
		return fmt.Errorf("blocked dial to non-public address %q (set allowPrivateNetworks=true only for local deployments)", address)
	}
	return nil
}

// isDisallowedIP reports whether ip must not be fetched from: loopback,
// private (RFC1918 / ULA), link-local (which covers the cloud metadata
// endpoint 169.254.169.254), multicast, unspecified or broadcast.
func isDisallowedIP(ip net.IP) bool {
	return ip.IsLoopback() || ip.IsPrivate() ||
		ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
		ip.IsMulticast() || ip.IsUnspecified() || ip.Equal(net.IPv4bcast)
}

// fetcher abstracts HTTP GET so tests can inject responses.
type fetcher func(ctx context.Context, url string) ([]byte, error)

// httpFetcher returns a fetcher backed by an http.Client with the configured
// timeout.
func httpFetcher(client *http.Client) fetcher {
	return func(ctx context.Context, url string) ([]byte, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Accept", "application/json, application/did+json, */*")
		resp, err := client.Do(req)
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close()
		body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		if err != nil {
			return nil, err
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return nil, fmt.Errorf("http %d for %s", resp.StatusCode, url)
		}
		return body, nil
	}
}

// statusFetcher performs an HTTP GET and returns the status code and body. It
// returns an error only on a transport failure (DNS, dial, timeout) — a non-2xx
// status is reported via the returned code, not as an error, so callers can
// distinguish e.g. 404 (record absent) from 200 (record present). Used by the
// DEDI revocation check, where record existence — not body content — is the
// signal.
type statusFetcher func(ctx context.Context, url string) (int, []byte, error)

// httpStatusFetcher returns a statusFetcher backed by an http.Client.
func httpStatusFetcher(client *http.Client) statusFetcher {
	return func(ctx context.Context, url string) (int, []byte, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return 0, nil, err
		}
		req.Header.Set("Accept", "application/json, */*")
		resp, err := client.Do(req)
		if err != nil {
			return 0, nil, err
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		return resp.StatusCode, body, nil
	}
}

// ---------------------------------------------------------------------------
// base58
// ---------------------------------------------------------------------------

// base58btc alphabet (Bitcoin).
const b58Alphabet = "123456789ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz"

var b58Index = func() [256]int8 {
	var idx [256]int8
	for i := range idx {
		idx[i] = -1
	}
	for i := 0; i < len(b58Alphabet); i++ {
		idx[b58Alphabet[i]] = int8(i)
	}
	return idx
}()

// base58Decode decodes a base58btc string into bytes, preserving leading
// '1' characters as leading zero bytes.
func base58Decode(s string) ([]byte, error) {
	if len(s) == 0 {
		return nil, fmt.Errorf("empty base58 string")
	}
	// count leading '1's -> leading zero bytes.
	zeros := 0
	for zeros < len(s) && s[zeros] == '1' {
		zeros++
	}
	// big-number base conversion.
	out := make([]byte, 0, len(s))
	for i := zeros; i < len(s); i++ {
		c := b58Index[s[i]]
		if c < 0 {
			return nil, fmt.Errorf("invalid base58 character %q", s[i])
		}
		carry := int(c)
		for j := 0; j < len(out); j++ {
			carry += 58 * int(out[j])
			out[j] = byte(carry & 0xff)
			carry >>= 8
		}
		for carry > 0 {
			out = append(out, byte(carry&0xff))
			carry >>= 8
		}
	}
	// out is little-endian; reverse and prepend leading zeros.
	res := make([]byte, zeros+len(out))
	for i := 0; i < len(out); i++ {
		res[zeros+i] = out[len(out)-1-i]
	}
	return res, nil
}
