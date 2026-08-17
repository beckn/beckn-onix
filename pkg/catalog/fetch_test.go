package catalog

// fetch_test.go — covers Fetcher end to end against httptest servers: index
// fetch with per-entry signature verification (and dropping a forged
// entry), file fetch with digest + signature + envelope-unwrap, the
// CON-TBD-12 cross-check, and conditional-GET (200-then-304).

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/beckn-one/beckn-onix/pkg/crawler"
	"github.com/beckn-one/beckn-onix/pkg/security/artifactverifier"
)

// testSigner signs fixtures the same way a publisher would: Ed25519 over the
// JCS canonicalization of the document with "signature" removed, matching
// what Fetcher verifies via crawler.VerifySignature.
type testSigner struct {
	pub  ed25519.PublicKey
	priv ed25519.PrivateKey
}

func newTestSigner(t *testing.T) testSigner {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	return testSigner{pub: pub, priv: priv}
}

func (s testSigner) keys() crawler.KeySource {
	return crawler.StaticKeys(map[string]ed25519.PublicKey{"key-1": s.pub})
}

// sign marshals fields, signs the canonical form with "signature" removed,
// and returns the final JSON with the signature filled in.
func (s testSigner) sign(t *testing.T, fields map[string]any) []byte {
	t.Helper()
	fields["signature"] = map[string]string{"keyId": "key-1"} // placeholder so canonicalization excludes only "signature"
	body, err := json.Marshal(fields)
	if err != nil {
		t.Fatal(err)
	}
	canonical, err := artifactverifier.CanonicalizeJCSExcluding(body, "signature")
	if err != nil {
		t.Fatal(err)
	}
	sigB64 := base64.StdEncoding.EncodeToString(ed25519.Sign(s.priv, canonical))
	fields["signature"] = map[string]string{"keyId": "key-1", "value": sigB64}
	out, err := json.Marshal(fields)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func (s testSigner) signEntry(t *testing.T, catalogID string, baseline map[string]any) []byte {
	t.Helper()
	return s.sign(t, map[string]any{
		"catalogId":   catalogID,
		"catalogType": "REGULAR",
		"status":      "ACTIVE",
		"baseline":    baseline,
		"changes":     []any{},
	})
}

func (s testSigner) signBaseline(t *testing.T, catalogID string, version int64, catalog map[string]any) []byte {
	t.Helper()
	return s.sign(t, map[string]any{
		"catalogId": catalogID,
		"version":   version,
		"catalog":   catalog,
	})
}

func sha256Prefixed(b []byte) string {
	sum := sha256.Sum256(b)
	return "sha-256:" + hex.EncodeToString(sum[:])
}

func newFetcher(signer testSigner) *Fetcher {
	client := crawler.NewClient(5*time.Second, 1<<20, true) // allowPrivate: httptest is 127.0.0.1
	return NewFetcher(client, signer.keys(), 1<<20)
}

func TestFetchIndex_VerifiesAndParsesEntries(t *testing.T) {
	signer := newTestSigner(t)
	entry := signer.signEntry(t, "p/c", map[string]any{"version": 1, "url": "https://x/b.json", "size": 10, "digest": "sha-256:abc"})
	index := fmt.Sprintf(`{"nodeId":"node-1","next_update":"2026-01-01T00:00:00Z","catalogs":[%s]}`, entry)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(index))
	}))
	defer srv.Close()

	f := newFetcher(signer)
	res, err := f.FetchIndex(context.Background(), srv.URL, IndexConditions{})
	if err != nil {
		t.Fatal(err)
	}
	if res.Index.NodeID != "node-1" {
		t.Fatalf("nodeId = %q, want node-1", res.Index.NodeID)
	}
	if len(res.Index.Catalogs) != 1 || res.Index.Catalogs[0].CatalogID != "p/c" {
		t.Fatalf("catalogs = %+v, want one entry p/c", res.Index.Catalogs)
	}
	if len(res.Dropped) != 0 {
		t.Fatalf("dropped = %+v, want none", res.Dropped)
	}
}

func TestFetchIndex_DropsUnverifiedEntry(t *testing.T) {
	signer := newTestSigner(t)
	good := signer.signEntry(t, "p/good", map[string]any{"version": 1, "url": "https://x/b.json", "size": 1, "digest": "sha-256:abc"})
	forged := `{"catalogId":"p/bad","catalogType":"REGULAR","status":"ACTIVE","baseline":{"version":1,"url":"https://x/b.json","size":1,"digest":"sha-256:abc"},"changes":[],"signature":{"keyId":"key-1","value":"forged=="}}`
	index := fmt.Sprintf(`{"nodeId":"node-1","catalogs":[%s,%s]}`, good, forged)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(index))
	}))
	defer srv.Close()

	f := newFetcher(signer)
	res, err := f.FetchIndex(context.Background(), srv.URL, IndexConditions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Index.Catalogs) != 1 || res.Index.Catalogs[0].CatalogID != "p/good" {
		t.Fatalf("catalogs = %+v, want only p/good", res.Index.Catalogs)
	}
	if len(res.Dropped) != 1 || res.Dropped[0].CatalogID != "p/bad" {
		t.Fatalf("dropped = %+v, want p/bad", res.Dropped)
	}
}

func TestFetchIndex_ConditionalGet_NotModified(t *testing.T) {
	signer := newTestSigner(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("If-None-Match") == `"v1"` {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		w.Header().Set("ETag", `"v1"`)
		w.Write([]byte(`{"nodeId":"node-1","catalogs":[]}`))
	}))
	defer srv.Close()

	f := newFetcher(signer)
	ctx := context.Background()
	first, err := f.FetchIndex(ctx, srv.URL, IndexConditions{})
	if err != nil {
		t.Fatal(err)
	}
	second, err := f.FetchIndex(ctx, srv.URL, IndexConditions{ETag: first.ETag})
	if err != nil {
		t.Fatal(err)
	}
	if !second.NotModified {
		t.Fatalf("second = %+v, want NotModified", second)
	}
}

func TestFetchFile_BaselineVerifiedAndUnwrapped(t *testing.T) {
	signer := newTestSigner(t)
	catalogContent := map[string]any{"id": "p/c", "resources": []any{}}
	baseline := signer.signBaseline(t, "p/c", 1, catalogContent)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write(baseline)
	}))
	defer srv.Close()

	f := newFetcher(signer)
	entry := FileEntry{URL: srv.URL, Version: 1, Digest: sha256Prefixed(baseline)}
	got, err := f.FetchFile(context.Background(), "node-1", "p/c", entry)
	if err != nil {
		t.Fatal(err)
	}
	var unwrapped map[string]any
	if err := json.Unmarshal(got, &unwrapped); err != nil {
		t.Fatal(err)
	}
	if unwrapped["id"] != "p/c" {
		t.Fatalf("unwrapped = %+v, want id p/c (envelope should be stripped)", unwrapped)
	}
}

func TestFetchFile_DigestMismatchIsPermanent(t *testing.T) {
	signer := newTestSigner(t)
	baseline := signer.signBaseline(t, "p/c", 1, map[string]any{"id": "p/c"})

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write(baseline)
	}))
	defer srv.Close()

	f := newFetcher(signer)
	entry := FileEntry{URL: srv.URL, Version: 1, Digest: "sha-256:deadbeef"}
	_, err := f.FetchFile(context.Background(), "node-1", "p/c", entry)
	if err == nil || !crawler.IsPermanent(err) {
		t.Fatalf("expected a permanent digest-mismatch error, got %v", err)
	}
	if crawler.PermanentClass(err) != crawler.FaultDigestMismatch {
		t.Fatalf("class = %q, want %q", crawler.PermanentClass(err), crawler.FaultDigestMismatch)
	}
}

// CON-TBD-12: a correctly signed file whose own declared version doesn't
// match what the index entry expected must be rejected, not silently
// accepted as if it were the referenced content.
func TestFetchFile_VersionMismatchIsRejected(t *testing.T) {
	signer := newTestSigner(t)
	baseline := signer.signBaseline(t, "p/c", 2, map[string]any{"id": "p/c"}) // signed as version 2

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write(baseline)
	}))
	defer srv.Close()

	f := newFetcher(signer)
	entry := FileEntry{URL: srv.URL, Version: 1, Digest: sha256Prefixed(baseline)} // index says version 1
	_, err := f.FetchFile(context.Background(), "node-1", "p/c", entry)
	if err == nil || !crawler.IsPermanent(err) {
		t.Fatalf("expected a permanent error, got %v", err)
	}
	if crawler.PermanentClass(err) != crawler.FaultDigestMismatch {
		t.Fatalf("class = %q, want %q (content-identity mismatch, not a signature failure)", crawler.PermanentClass(err), crawler.FaultDigestMismatch)
	}
}

func TestFetchFile_UnsignedIsRejected(t *testing.T) {
	unsigned := []byte(`{"catalogId":"p/c","version":1,"catalog":{"id":"p/c"}}`)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write(unsigned)
	}))
	defer srv.Close()

	f := newFetcher(newTestSigner(t))
	entry := FileEntry{URL: srv.URL, Version: 1, Digest: sha256Prefixed(unsigned)}
	_, err := f.FetchFile(context.Background(), "node-1", "p/c", entry)
	if err == nil || !crawler.IsPermanent(err) {
		t.Fatalf("expected a permanent signature error, got %v", err)
	}
}
