package artifactsigner

import (
	"crypto/ed25519"
	"encoding/base64"
	"testing"
	"time"

	"github.com/beckn-one/beckn-onix/pkg/security/artifactverifier"
)

func TestSignDetachedJWS_RoundTripsWithVerifier(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("generating key: %v", err)
	}

	// The unsigned document: "proof" is absent, added only after signing,
	// mirroring how a real manifest/index is built.
	doc := []byte(`{"keys":[{"kid":"k1"}],"files":[{"url":"https://example.com/index.json"}]}`)

	jws, err := SignDetachedJWS(doc, priv)
	if err != nil {
		t.Fatalf("signing: %v", err)
	}

	// The document as a verifier would actually receive it: with the proof
	// attached. Verification must still succeed, since the signing input is
	// reconstructed from the document with "proof" stripped, not from the
	// unsigned doc bytes directly.
	signed := []byte(`{"keys":[{"kid":"k1"}],"files":[{"url":"https://example.com/index.json"}],"proof":{"verification_method":"k1","jws":"` + jws + `"}}`)

	if err := artifactverifier.VerifyDetachedJWS(signed, jws, pub); err != nil {
		t.Fatalf("expected verification to succeed, got: %v", err)
	}
}

func TestSignDetachedJWS_TamperedDocumentFailsVerification(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("generating key: %v", err)
	}

	doc := []byte(`{"a":1}`)
	jws, err := SignDetachedJWS(doc, priv)
	if err != nil {
		t.Fatalf("signing: %v", err)
	}

	tampered := []byte(`{"a":2,"proof":{"jws":"` + jws + `"}}`)
	if err := artifactverifier.VerifyDetachedJWS(tampered, jws, pub); err == nil {
		t.Fatal("expected verification to fail for tampered document")
	}
}

func TestSignDetachedJWS_WrongKeyFailsVerification(t *testing.T) {
	_, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("generating key: %v", err)
	}
	otherPub, _, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("generating key: %v", err)
	}

	doc := []byte(`{"a":1}`)
	jws, err := SignDetachedJWS(doc, priv)
	if err != nil {
		t.Fatalf("signing: %v", err)
	}

	signed := []byte(`{"a":1,"proof":{"jws":"` + jws + `"}}`)
	if err := artifactverifier.VerifyDetachedJWS(signed, jws, otherPub); err == nil {
		t.Fatal("expected verification to fail against the wrong public key")
	}
}

func TestSignDetachedJWS_InvalidKeyLength(t *testing.T) {
	if _, err := SignDetachedJWS([]byte(`{}`), make(ed25519.PrivateKey, 10)); err == nil {
		t.Fatal("expected error for invalid private key length")
	}
}

func TestSignFileTuple_RoundTripsWithVerifier(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("generating key: %v", err)
	}
	validUntil := time.Now().Add(24 * time.Hour)

	sig, err := SignFileTuple("CAT-1", 2, "https://cdn.test/CAT-1.v2.json", "sha-256:abc", validUntil, priv)
	if err != nil {
		t.Fatalf("signing: %v", err)
	}

	if err := artifactverifier.VerifyFileTuple("CAT-1", 2, "https://cdn.test/CAT-1.v2.json", "sha-256:abc", validUntil, sig, pub); err != nil {
		t.Fatalf("expected verification to succeed: %v", err)
	}

	// Any field change must invalidate the signature -- proves the
	// signature is bound to the whole tuple, not just part of it.
	if err := artifactverifier.VerifyFileTuple("CAT-1", 3, "https://cdn.test/CAT-1.v2.json", "sha-256:abc", validUntil, sig, pub); err == nil {
		t.Error("expected verification to fail when version differs")
	}
	if err := artifactverifier.VerifyFileTuple("CAT-1", 2, "https://cdn.test/CAT-1.v2.json", "sha-256:different", validUntil, sig, pub); err == nil {
		t.Error("expected verification to fail when digest differs")
	}
}

func TestSignFileTuple_InvalidKeyLength(t *testing.T) {
	if _, err := SignFileTuple("CAT-1", 1, "url", "digest", time.Now(), make(ed25519.PrivateKey, 10)); err == nil {
		t.Fatal("expected error for invalid private key length")
	}
}

func TestSignJSON_RoundTrips(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("generating key: %v", err)
	}

	// "signature" is present but not yet meaningful -- SignJSON must strip
	// it before canonicalizing, the same non-circularity convention
	// SignDetachedJWS uses for "proof".
	doc := []byte(`{"catalogId":"CAT-1","status":"ACTIVE","signature":{}}`)

	sig, err := SignJSON(doc, "signature", priv)
	if err != nil {
		t.Fatalf("signing: %v", err)
	}
	if sig == "" {
		t.Fatal("expected a non-empty signature")
	}

	canonical, err := artifactverifier.CanonicalizeJCSExcluding(doc, "signature")
	if err != nil {
		t.Fatalf("canonicalizing: %v", err)
	}
	sigBytes, err := base64.StdEncoding.DecodeString(sig)
	if err != nil {
		t.Fatalf("decoding signature: %v", err)
	}
	if !ed25519.Verify(pub, canonical, sigBytes) {
		t.Fatal("expected signature to verify against the canonicalized document")
	}

	// Changing a sibling field (with "signature" still present but empty)
	// must invalidate the signature -- proves the whole document minus
	// "signature" is bound, not some fixed subset.
	tampered := []byte(`{"catalogId":"CAT-1","status":"RETIRED","signature":{}}`)
	tamperedCanonical, err := artifactverifier.CanonicalizeJCSExcluding(tampered, "signature")
	if err != nil {
		t.Fatalf("canonicalizing tampered doc: %v", err)
	}
	if ed25519.Verify(pub, tamperedCanonical, sigBytes) {
		t.Fatal("expected signature verification to fail for a tampered document")
	}
}

func TestSignJSON_InvalidKeyLength(t *testing.T) {
	if _, err := SignJSON([]byte(`{}`), "signature", make(ed25519.PrivateKey, 10)); err == nil {
		t.Fatal("expected error for invalid private key length")
	}
}
