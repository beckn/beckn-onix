package artifactverifier

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"math/big"
	"testing"
	"time"
)

func TestParsePublicKeyResponse_DeDiBase64RSA(t *testing.T) {
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("failed to generate RSA key: %v", err)
	}
	der, err := x509.MarshalPKIXPublicKey(&privateKey.PublicKey)
	if err != nil {
		t.Fatalf("failed to marshal public key: %v", err)
	}

	body := `{"data":{"details":{"keyType":"RSA","keyFormat":"base64","publicKey":"` + base64.StdEncoding.EncodeToString(der) + `"}}}`
	key, err := ParsePublicKeyResponse([]byte(body))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := key.(*rsa.PublicKey); !ok {
		t.Fatalf("expected *rsa.PublicKey, got %T", key)
	}
}

func TestParsePublicKeyResponse_DeDiSigningPublicKeyPath(t *testing.T) {
	publicKey, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("failed to generate Ed25519 key: %v", err)
	}

	body := `{"data":{"details":{"signing_public_key":"` + base64.StdEncoding.EncodeToString(publicKey) + `"}}}`
	key, err := ParsePublicKeyResponse([]byte(body))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := key.(ed25519.PublicKey); !ok {
		t.Fatalf("expected ed25519.PublicKey, got %T", key)
	}
}

func TestParsePublicKeyResponse_IgnoresUnexpectedNestedKeys(t *testing.T) {
	publicKey, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("failed to generate Ed25519 key: %v", err)
	}

	body := `{"unexpected":{"publicKey":"` + base64.StdEncoding.EncodeToString(publicKey) + `"}}`
	if _, err := ParsePublicKeyResponse([]byte(body)); err == nil {
		t.Fatal("expected error for public key outside accepted response paths")
	}
}

func TestVerifyDetachedArtifact_Ed25519(t *testing.T) {
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("failed to generate Ed25519 key: %v", err)
	}
	content := []byte("manifest: true")
	signature := ed25519.Sign(privateKey, content)

	if err := VerifyDetachedArtifact(
		content,
		[]byte(base64.StdEncoding.EncodeToString(signature)),
		[]byte(base64.StdEncoding.EncodeToString(publicKey)),
	); err != nil {
		t.Fatalf("VerifyDetachedArtifact() error = %v", err)
	}
}

func TestVerifyDetachedArtifact_RSAFromPEM(t *testing.T) {
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("failed to generate RSA key: %v", err)
	}
	content := []byte("package policy\nviolations := set()\n")
	sum := sha256.Sum256(content)
	signature, err := rsa.SignPKCS1v15(rand.Reader, privateKey, crypto.SHA256, sum[:])
	if err != nil {
		t.Fatalf("failed to sign content: %v", err)
	}

	der, err := x509.MarshalPKIXPublicKey(&privateKey.PublicKey)
	if err != nil {
		t.Fatalf("failed to marshal public key: %v", err)
	}
	publicPEM := pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der})

	if err := VerifyDetachedArtifact(content, signature, publicPEM); err != nil {
		t.Fatalf("VerifyDetachedArtifact() error = %v", err)
	}
}

// TestParseSignature_JSONBody tests ParseSignature with a JSON signature body.
func TestParseSignature_JSONBody(t *testing.T) {
	sig := []byte("test-signature-bytes")
	encoded := base64.StdEncoding.EncodeToString(sig)
	body := `{"signature": "` + encoded + `"}`

	result, err := ParseSignature([]byte(body))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(result) != string(sig) {
		t.Fatalf("expected %q, got %q", sig, result)
	}
}

// TestParseSignature_JSONBadBase64 tests ParseSignature returns an error for an invalid base64 JSON signature.
func TestParseSignature_JSONBadBase64(t *testing.T) {
	body := `{"signature": "!!!not-valid-base64!!!"}`
	_, err := ParseSignature([]byte(body))
	if err == nil {
		t.Fatal("expected error but got none")
	}
}

// TestParseSignature_EmptyBody tests ParseSignature returns an error for empty input.
func TestParseSignature_EmptyBody(t *testing.T) {
	_, err := ParseSignature([]byte(""))
	if err == nil {
		t.Fatal("expected error for empty body")
	}
}

// TestVerifyDetached_ECDSA tests VerifyDetached with an ECDSA public key.
func TestVerifyDetached_ECDSA(t *testing.T) {
	privateKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("failed to generate ECDSA key: %v", err)
	}
	content := []byte("ecdsa test content")
	sum := sha256.Sum256(content)
	signature, err := ecdsa.SignASN1(rand.Reader, privateKey, sum[:])
	if err != nil {
		t.Fatalf("failed to sign: %v", err)
	}
	if err := VerifyDetached(content, signature, &privateKey.PublicKey); err != nil {
		t.Fatalf("VerifyDetached() error = %v", err)
	}
}

// TestVerifyDetached_UnsupportedKey tests VerifyDetached returns an error for an unsupported key type.
func TestVerifyDetached_UnsupportedKey(t *testing.T) {
	err := VerifyDetached([]byte("content"), []byte("sig"), "not-a-key")
	if err == nil {
		t.Fatal("expected error for unsupported key type")
	}
}

// TestParsePublicKey_RSAPublicKeyPEM tests parsePublicKey with an RSA PUBLIC KEY PEM block.
func TestParsePublicKey_RSAPublicKeyPEM(t *testing.T) {
	rsaKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("failed to generate RSA key: %v", err)
	}
	der := x509.MarshalPKCS1PublicKey(&rsaKey.PublicKey)
	pemData := pem.EncodeToMemory(&pem.Block{Type: "RSA PUBLIC KEY", Bytes: der})

	key, err := parsePublicKey(pemData)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := key.(*rsa.PublicKey); !ok {
		t.Fatalf("expected *rsa.PublicKey, got %T", key)
	}
}

// TestParsePublicKey_CertificatePEM tests parsePublicKey with a certificate PEM.
func TestParsePublicKey_CertificatePEM(t *testing.T) {
	rsaKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("failed to generate RSA key: %v", err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
	}
	certDER, err := x509.CreateCertificate(rand.Reader, template, template, &rsaKey.PublicKey, rsaKey)
	if err != nil {
		t.Fatalf("failed to create certificate: %v", err)
	}
	pemData := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER})

	key, err := parsePublicKey(pemData)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := key.(*rsa.PublicKey); !ok {
		t.Fatalf("expected *rsa.PublicKey from certificate, got %T", key)
	}
}

// TestParsePublicKey_UnsupportedPEMType tests parsePublicKey returns an error for an unsupported PEM type.
func TestParsePublicKey_UnsupportedPEMType(t *testing.T) {
	pemData := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: []byte("dummy")})
	_, err := parsePublicKey(pemData)
	if err == nil {
		t.Fatal("expected error for unsupported PEM block type")
	}
}

// TestParsePublicKey_RawDER tests parsePublicKey with raw DER-encoded PKIX data.
func TestParsePublicKey_RawDER(t *testing.T) {
	rsaKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("failed to generate RSA key: %v", err)
	}
	der, err := x509.MarshalPKIXPublicKey(&rsaKey.PublicKey)
	if err != nil {
		t.Fatalf("failed to marshal key: %v", err)
	}

	key, err := parsePublicKey(der)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := key.(*rsa.PublicKey); !ok {
		t.Fatalf("expected *rsa.PublicKey, got %T", key)
	}
}

// TestParsePublicKey_InvalidData tests parsePublicKey returns an error for invalid data.
func TestParsePublicKey_InvalidData(t *testing.T) {
	_, err := parsePublicKey([]byte("not-a-public-key!!!"))
	if err == nil {
		t.Fatal("expected error for invalid public key data")
	}
}

// TestParsePublicKeyValue_UnsupportedFormat tests parsePublicKeyValue returns an error for an unsupported format.
func TestParsePublicKeyValue_UnsupportedFormat(t *testing.T) {
	_, err := parsePublicKeyValue("somevalue", "unknown-format")
	if err == nil {
		t.Fatal("expected error for unsupported format")
	}
}

// TestParsePublicKeyResponse_TopLevelSigningPublicKey tests ParsePublicKeyResponse with a top-level signing_public_key.
func TestParsePublicKeyResponse_TopLevelSigningPublicKey(t *testing.T) {
	publicKey, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("failed to generate Ed25519 key: %v", err)
	}
	body := `{"signing_public_key":"` + base64.StdEncoding.EncodeToString(publicKey) + `"}`
	key, err := ParsePublicKeyResponse([]byte(body))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := key.(ed25519.PublicKey); !ok {
		t.Fatalf("expected ed25519.PublicKey, got %T", key)
	}
}

// TestVerifyDetachedArtifact_InvalidSignatureBody tests VerifyDetachedArtifact returns an error for an empty signature body.
func TestVerifyDetachedArtifact_InvalidSignatureBody(t *testing.T) {
	err := VerifyDetachedArtifact([]byte("content"), []byte(""), []byte("key"))
	if err == nil {
		t.Fatal("expected error for empty signature body")
	}
}

// TestVerifyDetachedArtifact_InvalidPublicKeyBody tests VerifyDetachedArtifact returns an error for an invalid public key body.
func TestVerifyDetachedArtifact_InvalidPublicKeyBody(t *testing.T) {
	_, privateKey, _ := ed25519.GenerateKey(rand.Reader)
	content := []byte("content")
	sig := ed25519.Sign(privateKey, content)

	err := VerifyDetachedArtifact(content, sig, []byte("!!!garbage!!!"))
	if err == nil {
		t.Fatal("expected error for invalid public key body")
	}
}

// TestVerifyDetached_Ed25519_RejectsTamperedSignature tests that a tampered Ed25519 signature is rejected.
func TestVerifyDetached_Ed25519_RejectsTamperedSignature(t *testing.T) {
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("failed to generate Ed25519 key: %v", err)
	}
	content := []byte("manifest: true")
	sig := ed25519.Sign(privateKey, content)
	sig[0] ^= 0xFF // tamper
	if err := VerifyDetached(content, sig, publicKey); err == nil {
		t.Fatal("expected verification to fail for tampered Ed25519 signature")
	}
}

// TestVerifyDetached_RSA_RejectsTamperedSignature tests that a tampered RSA signature is rejected.
func TestVerifyDetached_RSA_RejectsTamperedSignature(t *testing.T) {
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("failed to generate RSA key: %v", err)
	}
	content := []byte("manifest: true")
	sum := sha256.Sum256(content)
	sig, err := rsa.SignPKCS1v15(rand.Reader, privateKey, crypto.SHA256, sum[:])
	if err != nil {
		t.Fatalf("failed to sign: %v", err)
	}
	sig[0] ^= 0xFF // tamper
	if err := VerifyDetached(content, sig, &privateKey.PublicKey); err == nil {
		t.Fatal("expected verification to fail for tampered RSA signature")
	}
}

// TestVerifyDetached_ECDSA_RejectsTamperedSignature tests that a tampered ECDSA signature is rejected.
func TestVerifyDetached_ECDSA_RejectsTamperedSignature(t *testing.T) {
	privateKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("failed to generate ECDSA key: %v", err)
	}
	content := []byte("manifest: true")
	sum := sha256.Sum256(content)
	sig, err := ecdsa.SignASN1(rand.Reader, privateKey, sum[:])
	if err != nil {
		t.Fatalf("failed to sign: %v", err)
	}
	sig[len(sig)-1] ^= 0xFF // tamper last byte of s integer
	if err := VerifyDetached(content, sig, &privateKey.PublicKey); err == nil {
		t.Fatal("expected verification to fail for tampered ECDSA signature")
	}
}

// TestVerifyDetached_WrongKey tests that a signature from one key is rejected when verified against another.
func TestVerifyDetached_WrongKey(t *testing.T) {
	_, privA, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("failed to generate key A: %v", err)
	}
	pubB, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("failed to generate key B: %v", err)
	}
	content := []byte("manifest: true")
	sig := ed25519.Sign(privA, content)
	if err := VerifyDetached(content, sig, pubB); err == nil {
		t.Fatal("expected verification to fail when signature is from a different key")
	}
}
