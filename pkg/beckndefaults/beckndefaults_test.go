package beckndefaults

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestLoad_ShippedBaseline verifies the embedded constants file loads and
// passes signature verification against the embedded public key.
func TestLoad_ShippedBaseline(t *testing.T) {
	bc, err := Load(context.Background(), true)
	require.NoError(t, err)
	require.NotNil(t, bc)

	assert.Equal(t, "v1", bc.Version)

	require.NotNil(t, bc.Locked, "locked section must be present")
	require.Contains(t, bc.Locked, "dediregistry")
	assert.NotEmpty(t, bc.Locked["dediregistry"]["url"])

	require.NotNil(t, bc.Overridable, "overridable section must be present")
	require.Contains(t, bc.Overridable, "schemav2validator")
	assert.NotEmpty(t, bc.Overridable["schemav2validator"]["type"])
	assert.NotEmpty(t, bc.Overridable["schemav2validator"]["location"])
}

// TestLoadAndVerify_ValidSignature confirms the shipped file verifies cleanly.
func TestLoadAndVerify_ValidSignature(t *testing.T) {
	bc, err := loadAndVerify(shippedConstants, shippedConstantsSig)
	require.NoError(t, err)
	assert.NotNil(t, bc)
}

// TestLoadAndVerify_TamperedContent confirms that modifying the file body
// invalidates the signature.
func TestLoadAndVerify_TamperedContent(t *testing.T) {
	tampered := append([]byte(nil), shippedConstants...)
	tampered[0] ^= 0xFF // flip a byte
	_, err := loadAndVerify(tampered, shippedConstantsSig)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "signature verification")
}

// TestLoadAndVerify_WrongKey confirms that a signature from a different key
// is rejected even if the file content is unmodified.
func TestLoadAndVerify_WrongKey(t *testing.T) {
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	wrongSig := base64.StdEncoding.EncodeToString(ed25519.Sign(priv, shippedConstants))
	_, err = loadAndVerify(shippedConstants, []byte(wrongSig))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "signature verification")
}

// TestLoadAndVerify_EmptyConstants confirms that an empty body is rejected.
func TestLoadAndVerify_EmptyConstants(t *testing.T) {
	_, err := loadAndVerify([]byte{}, shippedConstantsSig)
	require.Error(t, err)
}

// TestLoadAndVerify_FreshKeypair signs a minimal constants file with a fresh
// keypair and verifies that loadAndVerify accepts it when the public key matches.
func TestLoadAndVerify_FreshKeypair(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)

	content := []byte("becknConstantsVersion: \"v1\"\nlocked: {}\noverridable: {}\n")
	sig := base64.StdEncoding.EncodeToString(ed25519.Sign(priv, content))

	pkix, err := x509.MarshalPKIXPublicKey(pub)
	require.NoError(t, err)
	pubPEM := pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: pkix})

	// Swap the global public key temporarily
	orig := becknPublicKeyPEM
	becknPublicKeyPEM = pubPEM
	defer func() { becknPublicKeyPEM = orig }()

	bc, err := loadAndVerify(content, []byte(sig))
	require.NoError(t, err)
	assert.Equal(t, "v1", bc.Version)
}

// TestFetchFromURLs_SizeLimit verifies that fetchFromURLs rejects a response
// body that exceeds maxConstantsFileBytes.
func TestFetchFromURLs_SizeLimit(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(make([]byte, maxConstantsFileBytes+1))
	}))
	defer srv.Close()

	_, _, err := fetchFromURLs(context.Background(), srv.URL, srv.URL)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "exceeds maximum allowed size")
}

// TestFetchFromURLs_NonOKStatus verifies that a non-200 response is rejected.
func TestFetchFromURLs_NonOKStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	_, _, err := fetchFromURLs(context.Background(), srv.URL, srv.URL)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "404")
}
