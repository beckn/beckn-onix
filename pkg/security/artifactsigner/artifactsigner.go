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
	"fmt"

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
