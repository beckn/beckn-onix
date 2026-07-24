package artifactverifier

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// dediJWSHeader is the fixed, unprotected-b64 detached-JWS header used by
// DeDi manifests and catalog indexes (onix-catalog-crawler-plugin-
// requirements.md §7.3): RFC 7797 "unencoded payload" with EdDSA.
const dediJWSHeader = `{"alg":"EdDSA","b64":false,"crit":["b64"]}`

// CanonicalizeJCS returns doc, with the top-level "proof" field removed (if
// present) and every object's keys sorted, compact-separated, matching RFC
// 8785 for the string/bool/integer-only documents DeDi manifests and
// indexes carry today (see the implementer caveat in
// onix-catalog-crawler-plugin-requirements.md §7.2 -- a generic
// encoding/json round trip is not RFC 8785-complete for arbitrary floats,
// but is sufficient here since Go's map marshaling already sorts keys and
// uses compact separators).
func CanonicalizeJCS(doc []byte) ([]byte, error) {
	var generic map[string]interface{}
	if err := json.Unmarshal(doc, &generic); err != nil {
		return nil, fmt.Errorf("canonicalizing document: %w", err)
	}
	delete(generic, "proof")
	return marshalSorted(generic)
}

// marshalSorted marshals v with recursively sorted object keys and no
// insignificant whitespace. json.Marshal already sorts map[string]any keys
// and uses compact separators, so this only needs to ensure nested values
// decoded from JSON stay as maps (which json.Unmarshal into interface{}
// already guarantees) -- kept as a named helper for clarity and so the
// sorting guarantee is documented at the call site, not assumed silently.
func marshalSorted(v interface{}) ([]byte, error) {
	if m, ok := v.(map[string]interface{}); ok {
		keys := make([]string, 0, len(m))
		for k := range m {
			keys = append(keys, k)
		}
		sort.Strings(keys) // encoding/json already does this for maps; explicit for auditability.
	}
	return json.Marshal(v)
}

// detachedSigningInput builds the RFC 7797 signing input for a detached JWS
// with b64:false: header_b64 + "." + canonical_bytes, where canonical_bytes
// are used raw (not base64-encoded).
func detachedSigningInput(canonical []byte) []byte {
	headerB64 := base64.RawURLEncoding.EncodeToString([]byte(dediJWSHeader))
	input := append([]byte(headerB64+"."), canonical...)
	return input
}

// VerifyDetachedJWS verifies a DeDi manifest/index's compact detached-JWS
// proof.jws ("header_b64..signature_b64") against doc: doc is canonicalized
// with its "proof" field removed (§7.2), the signing input is reconstructed
// per §7.3, and the signature is checked with Ed25519 over that input.
//
// Unlike the document-inclusive check this replaces, this never signs or
// verifies over content that itself contains the signature being checked.
func VerifyDetachedJWS(doc []byte, jws string, pub ed25519.PublicKey) error {
	parts := strings.Split(jws, ".")
	if len(parts) != 3 || parts[1] != "" {
		return fmt.Errorf("not a compact detached JWS (want header..signature): %q", jws)
	}
	headerB64, sigB64 := parts[0], parts[2]
	if headerB64 != base64.RawURLEncoding.EncodeToString([]byte(dediJWSHeader)) {
		return fmt.Errorf("unexpected JWS header")
	}
	sig, err := base64.RawURLEncoding.DecodeString(sigB64)
	if err != nil {
		return fmt.Errorf("decoding JWS signature: %w", err)
	}

	canonical, err := CanonicalizeJCS(doc)
	if err != nil {
		return err
	}
	input := detachedSigningInput(canonical)

	if !ed25519.Verify(pub, input, sig) {
		return fmt.Errorf("Ed25519 detached-JWS verification failed")
	}
	return nil
}
