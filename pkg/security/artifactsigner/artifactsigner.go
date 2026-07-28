// Package artifactsigner produces DeDi-style detached-JWS proofs for
// manifests and catalog indexes, the signing-side counterpart to
// artifactverifier.VerifyDetachedJWS. See
// onix-catalog-crawler-plugin-requirements.md §7 for the algorithm both
// sides implement: JCS-canonicalize the document with "proof" removed,
// build an RFC 7797 (b64:false) detached-JWS signing input, sign with
// Ed25519.
package artifactsigner

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"time"

	"github.com/beckn-one/beckn-onix/pkg/security/artifactverifier"
)

// dediJWSHeader must match artifactverifier's, byte for byte -- the
// verifier rejects any other header value.
const dediJWSHeader = `{"alg":"EdDSA","b64":false,"crit":["b64"]}`

// SignDetachedJWS returns a compact detached-JWS string
// ("header_b64..signature_b64") over doc, per §7.3. doc must not yet carry
// a "proof" field the caller intends to sign over -- if it does, it is
// ignored (stripped before canonicalization), never included in the
// signing input, since a document cannot authentically sign its own
// eventual signature.
func SignDetachedJWS(doc []byte, priv ed25519.PrivateKey) (string, error) {
	if len(priv) != ed25519.PrivateKeySize {
		return "", fmt.Errorf("artifactsigner: invalid Ed25519 private key length %d", len(priv))
	}
	canonical, err := artifactverifier.CanonicalizeJCS(doc)
	if err != nil {
		return "", err
	}

	headerB64 := base64.RawURLEncoding.EncodeToString([]byte(dediJWSHeader))
	input := append([]byte(headerB64+"."), canonical...)
	sig := ed25519.Sign(priv, input)
	sigB64 := base64.RawURLEncoding.EncodeToString(sig)

	return headerB64 + ".." + sigB64, nil
}

// SignFileTuple signs a catalog-index file entry's tuple
// {catalogId, version, url, digest, validUntil} for the file spec's
// per-entry signature model ("The signed entry is a tuple, not a bare
// hash" -- binding the signature to exactly one file, in one role, within
// its validity window). Returns the base64-standard-encoded Ed25519
// signature. The doc explicitly allows this to equally be encoded as a
// detached JWS instead; a plain signature was chosen as the simpler of
// the two allowed encodings -- artifactverifier.VerifyFileTuple is the
// matching counterpart and must stay byte-for-byte in sync with this.
func SignFileTuple(catalogID string, version int, url, digest string, validUntil time.Time, priv ed25519.PrivateKey) (string, error) {
	if len(priv) != ed25519.PrivateKeySize {
		return "", fmt.Errorf("artifactsigner: invalid Ed25519 private key length %d", len(priv))
	}
	canonical, err := fileTupleBytes(catalogID, version, url, digest, validUntil)
	if err != nil {
		return "", err
	}
	sig := ed25519.Sign(priv, canonical)
	return base64.StdEncoding.EncodeToString(sig), nil
}

// fileTupleBytes builds the canonical bytes both SignFileTuple and
// artifactverifier.VerifyFileTuple sign/verify over. A map[string]any is
// used (rather than a struct) because Go's encoding/json already sorts
// map keys and uses compact separators, satisfying JCS for the
// string/int/timestamp-only fields this tuple carries (same reasoning as
// artifactverifier.CanonicalizeJCS's doc comment).
func fileTupleBytes(catalogID string, version int, url, digest string, validUntil time.Time) ([]byte, error) {
	tuple := map[string]any{
		"catalogId":  catalogID,
		"version":    version,
		"url":        url,
		"digest":     digest,
		"validUntil": validUntil,
	}
	canonical, err := json.Marshal(tuple)
	if err != nil {
		return nil, fmt.Errorf("artifactsigner: canonicalizing signature tuple: %w", err)
	}
	return canonical, nil
}
