package fetch

// client_test.go — covers the retrieval client end-to-end against httptest
// servers: index/file fetch, gzip decode, signature + digest rejection,
// conditional-GET (200-then-304) round-trips, and the SSRF guard.

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/beckn-one/beckn-onix/pkg/plugin/implementation/crawler/internal/crawler/catalog"
)

func gz(t *testing.T, b []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := gzip.NewWriter(&buf)
	if _, err := w.Write(b); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func sha256Prefixed(s string) string {
	sum := sha256.Sum256([]byte(s))
	return "sha-256:" + hex.EncodeToString(sum[:])
}

func TestHTTPClient_FetchIndexAndFile(t *testing.T) {
	signer := newTestSigner(t)
	cat := string(signer.signChangeFile(t, "p/c", 0, 1))
	mux := http.NewServeMux()
	mux.HandleFunc("/index", func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(`{"nodeId":"p","version":7,"catalogs":[]}`))
	})
	mux.HandleFunc("/file", func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(cat))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	// allowPrivate for httptest (127.0.0.1); the trusted key is the signature gate's anchor.
	c := NewClient(5*time.Second, 1<<20, 1<<20, true, WithTrustedKeys(signer.source()))
	ctx := context.Background()

	res, err := c.FetchIndex(ctx, srv.URL+"/index", catalog.IndexConditions{})
	if err != nil {
		t.Fatal(err)
	}
	if res.Index.NodeID != "p" {
		t.Fatalf("index = %+v, want nodeId p", res.Index)
	}

	good := catalog.FileEntry{URL: srv.URL + "/file", Digest: sha256Prefixed(cat), Version: 1}
	body, err := c.FetchFile(ctx, "p", "p/c", good)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != cat {
		t.Fatalf("file body = %q, want %q", body, cat)
	}

	// A digest the bytes don't match, correctly signed: the digest gate still bites.
	bad := catalog.FileEntry{URL: srv.URL + "/file", Digest: "sha-256:deadbeef"}
	if _, err := c.FetchFile(ctx, "p", "p/c", bad); err == nil {
		t.Fatal("expected digest-mismatch error")
	}
}

// FetchIndex drops any catalog entry whose own self-signature does not verify
// -- fail closed rather than trust an unsigned or forged index entry, per the
// file spec's "each catalog entry signs itself".
func TestHTTPClient_FetchIndex_DropsUnverifiedEntries(t *testing.T) {
	signer := newTestSigner(t)
	good := string(signer.signEntry(t, "p/c1"))
	bad := `{"catalogId":"p/c2","status":"ACTIVE","baseline":{"version":1,"url":"https://x/b.json"},"changes":[],"signature":{"keyId":"pub-key-1","value":"forged"}}`
	index := `{"nodeId":"p","version":2,"catalogs":[` + good + `,` + bad + `]}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(index))
	}))
	defer srv.Close()

	c := NewClient(5*time.Second, 1<<20, 1<<20, true, WithTrustedKeys(signer.source()))
	res, err := c.FetchIndex(context.Background(), srv.URL, catalog.IndexConditions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Index.Catalogs) != 1 || res.Index.Catalogs[0].CatalogID != "p/c1" {
		t.Fatalf("catalogs = %+v, want only the validly-signed p/c1 entry", res.Index.Catalogs)
	}
}

// The signature gate runs AFTER decode (the embedded signature covers the
// document as authored, not its at-rest packaging), and fails closed: an
// unsigned file — or any file when no trusted keys were injected — is
// rejected.
func TestHTTPClient_FetchFile_SignatureGate(t *testing.T) {
	unsigned := `{"catalogId":"p/c","fromVersion":0,"toVersion":1,"resources":{},"offers":{}}`
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.Write([]byte(unsigned))
	}))
	defer srv.Close()

	signer := newTestSigner(t)
	ctx := context.Background()
	plain := catalog.FileEntry{URL: srv.URL + "/file", Digest: sha256Prefixed(unsigned)}

	t.Run("unsigned file", func(t *testing.T) {
		c := NewClient(5*time.Second, 1<<20, 1<<20, true, WithTrustedKeys(signer.source()))
		_, err := c.FetchFile(ctx, "p", "p/c", plain)
		assertPermanentFault(t, err, faultSignature)
	})

	t.Run("no trusted keys injected", func(t *testing.T) {
		c := NewClient(5*time.Second, 1<<20, 1<<20, true)
		_, err := c.FetchFile(ctx, "p", "p/c", plain)
		assertPermanentFault(t, err, faultSignature)
	})

	if hits.Load() != 2 {
		t.Fatalf("server was contacted %d times, want 2 (the signature gate runs after the GET, on the decoded content)", hits.Load())
	}
}

func TestHTTPClient_FetchGzipFile(t *testing.T) {
	signer := newTestSigner(t)
	cat := string(signer.signChangeFile(t, "p/c", 0, 1))
	compressed := gz(t, []byte(cat))
	// RFC NFH-014 CON-TBD-29: the digest covers the canonical DECOMPRESSED
	// content, never the compressed bytes at rest.
	digest := sha256Prefixed(cat)

	mux := http.NewServeMux()
	mux.HandleFunc("/c.json.gzip", func(w http.ResponseWriter, _ *http.Request) { w.Write(compressed) })
	mux.HandleFunc("/c", func(w http.ResponseWriter, _ *http.Request) { w.Write(compressed) })
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := NewClient(5*time.Second, 1<<20, 1<<20, true, WithTrustedKeys(signer.source()))
	ctx := context.Background()

	// Encoding inferred from the .json.gzip suffix.
	body, err := c.FetchFile(ctx, "p", "p/c", catalog.FileEntry{URL: srv.URL + "/c.json.gzip", Digest: digest, Version: 1})
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != cat {
		t.Fatalf("suffix-decoded = %q, want %q", body, cat)
	}

	// Encoding taken from the explicit FileEntry.Encoding on a plain URL.
	body, err = c.FetchFile(ctx, "p", "p/c", catalog.FileEntry{URL: srv.URL + "/c", Encoding: "gzip", Digest: digest, Version: 1})
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != cat {
		t.Fatalf("explicit-encoding decoded = %q, want %q", body, cat)
	}
}

// Regression: a digest computed over the compressed bytes at rest (the old,
// spec-violating behavior) must be REJECTED, not accepted — CON-TBD-29
// requires the digest to cover the decompressed content only. This is the
// exact bug where every gzip-served file digest-mismatched (or, if the
// digest were wrongly computed the same wrong way on both sides, silently
// verified against the wrong thing).
func TestHTTPClient_FetchGzipFile_DigestOverCompressedBytesRejected(t *testing.T) {
	signer := newTestSigner(t)
	cat := string(signer.signChangeFile(t, "p/c", 0, 1))
	compressed := gz(t, []byte(cat))
	wrongDigest := sha256Prefixed(string(compressed)) // over compressed bytes -- wrong per CON-TBD-29

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.Write(compressed) }))
	defer srv.Close()

	c := NewClient(5*time.Second, 1<<20, 1<<20, true, WithTrustedKeys(signer.source()))
	_, err := c.FetchFile(context.Background(), "p", "p/c", catalog.FileEntry{URL: srv.URL + "/c.json.gz", Digest: wrongDigest, Version: 1})
	assertPermanentFault(t, err, catalog.FaultDigestMismatch)
}

// The conditional-GET round-trip: a 200 captures the host's validators; echoing
// them back yields a 304 with no body (the bandwidth saving), and the validators
// are preserved so they stay stored for next time.
func TestHTTPClient_FetchIndex_Conditional(t *testing.T) {
	const etag = `W/"v7"`
	const lastMod = "Wed, 21 Oct 2026 07:28:00 GMT"
	index := `{"nodeId":"p","version":7,"catalogs":[]}`

	mux := http.NewServeMux()
	mux.HandleFunc("/index", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("If-None-Match") == etag {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		w.Header().Set("ETag", etag)
		w.Header().Set("Last-Modified", lastMod)
		w.Write([]byte(index))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := NewClient(5*time.Second, 1<<20, 1<<20, true)
	ctx := context.Background()

	// First fetch (no validator) → 200, and we capture ETag / Last-Modified.
	res, err := c.FetchIndex(ctx, srv.URL+"/index", catalog.IndexConditions{})
	if err != nil {
		t.Fatal(err)
	}
	if res.NotModified {
		t.Fatal("first fetch must not be NotModified")
	}
	if res.ETag != etag || res.LastModified != lastMod {
		t.Fatalf("first fetch = %+v, want version 7 + captured ETag/Last-Modified", res)
	}

	// Second fetch echoing the ETag → server answers 304, no body downloaded.
	res2, err := c.FetchIndex(ctx, srv.URL+"/index", catalog.IndexConditions{ETag: res.ETag, LastModified: res.LastModified})
	if err != nil {
		t.Fatal(err)
	}
	if !res2.NotModified {
		t.Fatal("fetch with matching ETag must be NotModified")
	}
	if res2.ETag != etag {
		t.Fatalf("304 must echo the ETag so it stays stored, got %q", res2.ETag)
	}
}

// A host that sends no validators still works: the fetch is a plain 200 with
// empty ETag/Last-Modified, and the caller falls back to its version gate.
func TestHTTPClient_FetchIndex_NoValidators(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/index", func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(`{"nodeId":"p","version":3,"catalogs":[]}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := NewClient(5*time.Second, 1<<20, 1<<20, true)
	res, err := c.FetchIndex(context.Background(), srv.URL+"/index", catalog.IndexConditions{ETag: `W/"stale"`})
	if err != nil {
		t.Fatal(err)
	}
	if res.NotModified || res.Index.NodeID != "p" || res.ETag != "" {
		t.Fatalf("no-validator host = %+v, want a plain 200 (version 3, empty ETag)", res)
	}
}

func TestCheckPublicURL(t *testing.T) {
	ctx := context.Background()
	reject := []string{"http://127.0.0.1/x", "http://10.0.0.1/x", "http://192.168.1.1/x", "ftp://example.com/x"}
	for _, u := range reject {
		if err := checkPublicURL(ctx, u); err == nil {
			t.Errorf("checkPublicURL(%q) = nil, want error", u)
		}
	}
	if err := checkPublicURL(ctx, "https://93.184.216.34/x"); err != nil {
		t.Errorf("checkPublicURL(public IP) = %v, want nil", err)
	}
}

// The guard's DNS lookup must be bound to the caller's context. The host comes
// from an untrusted publisher, so a resolver that never answers would otherwise
// stall the check indefinitely and escape FetchTimeout entirely.
func TestCheckPublicURL_ResolverBoundedByContext(t *testing.T) {
	orig := resolveIPAddr
	t.Cleanup(func() { resolveIPAddr = orig })
	resolveIPAddr = func(ctx context.Context, _ string) ([]net.IPAddr, error) {
		<-ctx.Done() // a hostile/hung resolver: it never answers
		return nil, ctx.Err()
	}

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- checkPublicURL(ctx, "https://slow.example/x") }()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("checkPublicURL with a hung resolver = nil, want an error")
		}
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("checkPublicURL = %v, want it to surface the context deadline", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("checkPublicURL did not return: the resolver is not bounded by the context")
	}
}

// The fetch package applies its own FetchTimeout deadline at the top of the
// request path, so a hung resolver is bounded whatever context the caller
// passes. This is the production shape of the case above: no caller supplies a
// deadline (the engine passes its unbounded Start context) and
// http.Client.Timeout cannot cover the SSRF pre-check, which runs before the
// request object exists. Unbounded, one publisher with a dead resolver stalls
// the index pass while holding its lock, or stalls the whole queue drain.
//
// Note what this test does NOT do: it passes context.Background() and creates no
// deadline of its own. The bound has to come from the client.
func TestClient_FetchTimeoutBoundsHungResolver(t *testing.T) {
	orig := resolveIPAddr
	t.Cleanup(func() { resolveIPAddr = orig })
	resolveIPAddr = func(ctx context.Context, _ string) ([]net.IPAddr, error) {
		<-ctx.Done() // a hostile/hung resolver: it never answers
		return nil, ctx.Err()
	}

	signer := newTestSigner(t)
	// allowPrivate=false so the URL-level pre-check (and its DNS lookup) runs.
	c := NewClient(50*time.Millisecond, 1<<20, 1<<20, false, WithTrustedKeys(signer.source()))
	entry := catalog.FileEntry{
		URL:    "https://hung.example/c/v1.json",
		Digest: sha256Prefixed("anything"),
	}

	tests := []struct {
		name string
		call func(ctx context.Context) error
	}{
		{
			name: "FetchIndex",
			call: func(ctx context.Context) error {
				_, err := c.FetchIndex(ctx, "https://hung.example/index.json", catalog.IndexConditions{})
				return err
			},
		},
		{
			name: "FetchFile",
			call: func(ctx context.Context) error {
				_, err := c.FetchFile(ctx, "p", "p/c", entry)
				return err
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			done := make(chan error, 1)
			// context.Background() stands in for the engine's Start context: no
			// deadline, no cancellation.
			go func() { done <- tt.call(context.Background()) }()
			select {
			case err := <-done:
				if err == nil {
					t.Fatal("hung resolver = nil error, want a deadline failure")
				}
				if !errors.Is(err, context.DeadlineExceeded) {
					t.Fatalf("err = %v, want it to surface the client's own FetchTimeout deadline", err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("call did not return: FetchTimeout is not applied as a context deadline")
			}
		})
	}
}

// A non-positive timeout means "no limit", matching http.Client.Timeout, and
// must not turn into an instantly-expired deadline that fails every fetch.
func TestClient_ZeroTimeoutMeansNoDeadline(t *testing.T) {
	signer := newTestSigner(t)
	cat := string(signer.signChangeFile(t, "p/c", 0, 1))
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(cat))
	}))
	defer srv.Close()

	c := NewClient(0, 1<<20, 1<<20, true, WithTrustedKeys(signer.source()))
	f := catalog.FileEntry{URL: srv.URL + "/file", Digest: sha256Prefixed(cat), Version: 1}
	body, err := c.FetchFile(context.Background(), "p", "p/c", f)
	if err != nil {
		t.Fatalf("FetchFile with timeout 0 = %v, want it to succeed", err)
	}
	if string(body) != cat {
		t.Fatalf("body = %q, want %q", body, cat)
	}
}

// The fetch layer's integrity/guard rejections must be PERMANENT and carry their
// own FaultClass. Raised as plain errors they classified as `transient`, so the
// runner re-fetched the same bad bytes every ~5 min forever (never parking, never
// alerting) and mislabeled the cause — for a digest mismatch, the only integrity
// gate Phase 1 has.
func TestFetchFaults_ArePermanentAndClassified(t *testing.T) {
	signer := newTestSigner(t)
	cat := string(signer.signChangeFile(t, "p/c", 0, 1))
	mux := http.NewServeMux()
	mux.HandleFunc("/file", func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte(cat)) })
	srv := httptest.NewServer(mux)
	defer srv.Close()
	ctx := context.Background()
	keys := WithTrustedKeys(signer.source())

	t.Run("digest mismatch", func(t *testing.T) {
		c := NewClient(5*time.Second, 1<<20, 1<<20, true, keys)
		f := catalog.FileEntry{URL: srv.URL + "/file", Digest: sha256Prefixed("something else")}
		_, err := c.FetchFile(ctx, "p", "p/c", f)
		assertPermanentFault(t, err, catalog.FaultDigestMismatch)
	})

	t.Run("artifact over the compressed cap", func(t *testing.T) {
		c := NewClient(5*time.Second, 8, 1<<20, true, keys) // maxBytes=8 < len(cat)
		f := catalog.FileEntry{URL: srv.URL + "/file", Digest: sha256Prefixed(cat)}
		_, err := c.FetchFile(ctx, "p", "p/c", f)
		assertPermanentFault(t, err, catalog.FaultOversize)
	})

	t.Run("ssrf rejection", func(t *testing.T) {
		assertPermanentFault(t, checkPublicURL(ctx, "http://10.0.0.1/x"), catalog.FaultSSRF)
	})

	// Counter-case: a 5xx must stay transient, or a publisher blip would park the
	// catalog until they publish a new version.
	t.Run("5xx stays transient", func(t *testing.T) {
		down := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusServiceUnavailable)
		}))
		defer down.Close()
		c := NewClient(5*time.Second, 1<<20, 1<<20, true, keys)
		f := catalog.FileEntry{URL: down.URL, Digest: sha256Prefixed(cat)}
		_, err := c.FetchFile(ctx, "p", "p/c", f)
		if err == nil {
			t.Fatal("want an error on 503")
		}
		if catalog.IsPermanent(err) {
			t.Fatalf("503 must stay transient (retryable), got permanent: %v", err)
		}
		if got := catalog.ClassifyFault(0, err); got != catalog.FaultTransient {
			t.Fatalf("ClassifyFault(503) = %q, want %q", got, catalog.FaultTransient)
		}
	})
}

func assertPermanentFault(t *testing.T, err error, want catalog.FaultClass) {
	t.Helper()
	if err == nil {
		t.Fatalf("want a %q error, got nil", want)
	}
	if !catalog.IsPermanent(err) {
		t.Fatalf("%v: must be permanent (park), not transient (retry forever)", err)
	}
	if got := catalog.ClassifyFault(0, err); got != want {
		t.Fatalf("ClassifyFault(%v) = %q, want %q", err, got, want)
	}
	if !want.Permanent() {
		t.Fatalf("FaultClass %q must be Permanent() for the runner to park it", want)
	}
}
