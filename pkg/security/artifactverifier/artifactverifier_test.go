package artifactverifier

import (
	"crypto"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"testing"
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
