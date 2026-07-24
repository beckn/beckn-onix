package artifactsigner

import (
	"crypto/ed25519"
	"testing"

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
