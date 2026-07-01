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

// TestFetchFromURLs_HappyPath verifies both response bodies are returned on success.
func TestFetchFromURLs_HappyPath(t *testing.T) {
	constantsBody := []byte("constants-content")
	sigBody := []byte("sig-content")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/constants":
			w.Write(constantsBody)
		case "/sig":
			w.Write(sigBody)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	gotConstants, gotSig, err := fetchFromURLs(context.Background(), srv.URL+"/constants", srv.URL+"/sig")
	require.NoError(t, err)
	assert.Equal(t, constantsBody, gotConstants)
	assert.Equal(t, sigBody, gotSig)
}

// TestFetchFromURLs_SigURLFails verifies that a sig-URL failure is returned even when the constants URL succeeds.
func TestFetchFromURLs_SigURLFails(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/constants" {
			w.Write([]byte("constants-content"))
		} else {
			w.WriteHeader(http.StatusInternalServerError)
		}
	}))
	defer srv.Close()

	_, _, err := fetchFromURLs(context.Background(), srv.URL+"/constants", srv.URL+"/sig")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "500")
}

// TestFetchRemote_CancelledContext verifies that a cancelled context is propagated as an error.
func TestFetchRemote_CancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, _, err := fetchRemote(ctx)
	require.Error(t, err)
}

// TestLoad_RemoteUnavailable_FallsBackToShipped verifies that a remote fetch failure falls back to the shipped baseline.
func TestLoad_RemoteUnavailable_FallsBackToShipped(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	bc, err := Load(ctx, false)
	require.NoError(t, err)
	require.NotNil(t, bc)
	assert.Equal(t, "v1", bc.Version)
}

// TestLoad_RemoteRefresh exercises the full remote-fetch path; skipped under -short.
func TestLoad_RemoteRefresh(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping network test in short mode")
	}

	bc, err := Load(context.Background(), false)
	require.NoError(t, err)
	require.NotNil(t, bc)
	assert.Equal(t, "v1", bc.Version)
}

// TestLoad_ShippedConstantsTampered verifies that tampered shipped constants are rejected with an error.
func TestLoad_ShippedConstantsTampered(t *testing.T) {
	orig := shippedConstants
	shippedConstants = []byte("this-content-will-not-verify")
	defer func() { shippedConstants = orig }()

	_, err := Load(context.Background(), true)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "shipped beckn constants failed verification")
}

// TestFetchFromURLs_InvalidURL verifies that a malformed URL is rejected.
func TestFetchFromURLs_InvalidURL(t *testing.T) {
	_, _, err := fetchFromURLs(context.Background(), "://invalid-url", "://invalid-url")
	require.Error(t, err)
}

// TestLoadAndVerify_InvalidYAML verifies that invalid YAML content is rejected even when the signature is valid.
func TestLoadAndVerify_InvalidYAML(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)

	content := []byte("key: :\n  - bad: yaml: :\n")
	sig := base64.StdEncoding.EncodeToString(ed25519.Sign(priv, content))

	pkix, err := x509.MarshalPKIXPublicKey(pub)
	require.NoError(t, err)
	pubPEM := pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: pkix})

	orig := becknPublicKeyPEM
	becknPublicKeyPEM = pubPEM
	defer func() { becknPublicKeyPEM = orig }()

	_, err = loadAndVerify(content, []byte(sig))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "parse")
}
