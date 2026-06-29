package vcvalidator

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/decred/dcrd/dcrec/secp256k1/v4"
	"github.com/lestrrat-go/jwx/v3/jwa"
	"github.com/lestrrat-go/jwx/v3/jwk"
)

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
	case hasPrefix(raw, mcEd25519):
		k := raw[len(mcEd25519):]
		if len(k) != ed25519.PublicKeySize {
			return nil, fmt.Errorf("ed25519 key wrong size: %d", len(k))
		}
		return &resolvedKey{pub: ed25519.PublicKey(k), alg: jwa.EdDSA()}, nil
	case hasPrefix(raw, mcP256):
		pub, err := decompressP256(raw[len(mcP256):])
		if err != nil {
			return nil, err
		}
		return &resolvedKey{pub: pub, alg: jwa.ES256()}, nil
	case hasPrefix(raw, mcSecp256k1):
		pk, err := secp256k1.ParsePubKey(raw[len(mcSecp256k1):])
		if err != nil {
			return nil, fmt.Errorf("secp256k1 parse: %w", err)
		}
		return &resolvedKey{pub: pk.ToECDSA(), alg: jwa.ES256K()}, nil
	default:
		return nil, fmt.Errorf("unsupported multicodec prefix %x", firstN(raw, 2))
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

func hasPrefix(b, prefix []byte) bool {
	if len(b) < len(prefix) {
		return false
	}
	for i := range prefix {
		if b[i] != prefix[i] {
			return false
		}
	}
	return true
}

func firstN(b []byte, n int) []byte {
	if len(b) < n {
		return b
	}
	return b[:n]
}
