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
	cat := `{"id":"p/c","resources":[{"id":"r1"}]}`
	mux := http.NewServeMux()
	mux.HandleFunc("/index", func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(`{"participantId":"p","version":7,"catalogs":[]}`))
	})
	mux.HandleFunc("/file", func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(cat))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	signer := newTestSigner(t)
	// allowPrivate for httptest (127.0.0.1); the trusted key is the signature gate's anchor.
	c := NewClient(5*time.Second, 1<<20, 1<<20, true, WithTrustedKeys(signer.source()))
	ctx := context.Background()

	res, err := c.FetchIndex(ctx, srv.URL+"/index", catalog.IndexConditions{})
	if err != nil {
		t.Fatal(err)
	}
	if res.Index.ParticipantID != "p" || res.Index.Version != 7 {
		t.Fatalf("index = %+v, want participantId p version 7", res.Index)
	}

	good := signer.sign(t, "p/c", catalog.FileEntry{URL: srv.URL + "/file", Digest: sha256Prefixed(cat)})
	body, err := c.FetchFile(ctx, good)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != cat {
		t.Fatalf("file body = %q, want %q", body, cat)
	}

	// A digest the bytes don't match, correctly signed: the digest gate still bites.
	bad := signer.sign(t, "p/c", catalog.FileEntry{URL: srv.URL + "/file", Digest: "sha-256:deadbeef"})
	if _, err := c.FetchFile(ctx, bad); err == nil {
		t.Fatal("expected digest-mismatch error")
	}
}

// FetchIndex stamps each file entry with its enclosing catalog's id: the signed
// tuple covers catalogId, but the wire format leaves it implicit in the nesting,
// so an unstamped entry could never be verified once it travels on its own.
func TestHTTPClient_FetchIndex_StampsCatalogID(t *testing.T) {
	index := `{"participantId":"p","version":2,"catalogs":[
		{"catalogId":"p/c1","baseline":{"version":1,"url":"https://x/b.json"},
		 "changes":[{"version":2,"url":"https://x/c.json"}]}]}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(index))
	}))
	defer srv.Close()

	c := NewClient(5*time.Second, 1<<20, 1<<20, true)
	res, err := c.FetchIndex(context.Background(), srv.URL, catalog.IndexConditions{})
	if err != nil {
		t.Fatal(err)
	}
	entry := res.Index.Catalogs[0]
	if got := entry.Baseline.Signature.CatalogID; got != "p/c1" {
		t.Errorf("baseline CatalogID = %q, want %q", got, "p/c1")
	}
	if got := entry.Changes[0].Signature.CatalogID; got != "p/c1" {
		t.Errorf("change CatalogID = %q, want %q", got, "p/c1")
	}
}

// The signature gate runs BEFORE the GET, and fails closed: an unsigned entry —
// or any entry when no trusted keys were injected — is rejected without the
// server ever being contacted.
func TestHTTPClient_FetchFile_SignatureGate(t *testing.T) {
	cat := `{"id":"p/c","resources":[{"id":"r1"}]}`
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.Write([]byte(cat))
	}))
	defer srv.Close()

	signer := newTestSigner(t)
	ctx := context.Background()
	plain := catalog.FileEntry{URL: srv.URL + "/file", Digest: sha256Prefixed(cat)}

	t.Run("unsigned entry", func(t *testing.T) {
		c := NewClient(5*time.Second, 1<<20, 1<<20, true, WithTrustedKeys(signer.source()))
		_, err := c.FetchFile(ctx, plain)
		assertPermanentFault(t, err, faultSignature)
	})

	t.Run("no trusted keys injected", func(t *testing.T) {
		c := NewClient(5*time.Second, 1<<20, 1<<20, true)
		_, err := c.FetchFile(ctx, signer.sign(t, "p/c", plain))
		assertPermanentFault(t, err, faultSignature)
	})

	if hits.Load() != 0 {
		t.Fatalf("server was contacted %d times; the signature gate must reject before the GET", hits.Load())
	}
}

func TestHTTPClient_FetchGzipFile(t *testing.T) {
	cat := `{"id":"p/c","resources":[{"id":"r1"}]}`
	compressed := gz(t, []byte(cat))
	// The digest covers the artifact AT REST — the compressed bytes we hash
	// before spending CPU inflating.
	digest := sha256Prefixed(string(compressed))

	mux := http.NewServeMux()
	mux.HandleFunc("/c.json.gzip", func(w http.ResponseWriter, _ *http.Request) { w.Write(compressed) })
	mux.HandleFunc("/c", func(w http.ResponseWriter, _ *http.Request) { w.Write(compressed) })
	srv := httptest.NewServer(mux)
	defer srv.Close()

	signer := newTestSigner(t)
	c := NewClient(5*time.Second, 1<<20, 1<<20, true, WithTrustedKeys(signer.source()))
	ctx := context.Background()

	// Encoding inferred from the .json.gzip suffix.
	body, err := c.FetchFile(ctx, signer.sign(t, "p/c", catalog.FileEntry{URL: srv.URL + "/c.json.gzip", Digest: digest}))
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != cat {
		t.Fatalf("suffix-decoded = %q, want %q", body, cat)
	}

	// Encoding taken from the explicit FileEntry.Encoding on a plain URL.
	body, err = c.FetchFile(ctx, signer.sign(t, "p/c", catalog.FileEntry{URL: srv.URL + "/c", Encoding: "gzip", Digest: digest}))
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != cat {
		t.Fatalf("explicit-encoding decoded = %q, want %q", body, cat)
	}
}

// The conditional-GET round-trip: a 200 captures the host's validators; echoing
// them back yields a 304 with no body (the bandwidth saving), and the validators
// are preserved so they stay stored for next time.
func TestHTTPClient_FetchIndex_Conditional(t *testing.T) {
	const etag = `W/"v7"`
	const lastMod = "Wed, 21 Oct 2026 07:28:00 GMT"
	index := `{"participantId":"p","version":7,"catalogs":[]}`

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
	if res.Index.Version != 7 || res.ETag != etag || res.LastModified != lastMod {
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
		w.Write([]byte(`{"participantId":"p","version":3,"catalogs":[]}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := NewClient(5*time.Second, 1<<20, 1<<20, true)
	res, err := c.FetchIndex(context.Background(), srv.URL+"/index", catalog.IndexConditions{ETag: `W/"stale"`})
	if err != nil {
		t.Fatal(err)
	}
	if res.NotModified || res.Index.Version != 3 || res.ETag != "" {
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
	entry := signer.sign(t, "p/c", catalog.FileEntry{
		URL:    "https://hung.example/c/v1.json",
		Digest: sha256Prefixed("anything"),
	})

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
				_, err := c.FetchFile(ctx, entry)
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
	cat := `{"id":"p/c","resources":[{"id":"r1"}]}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(cat))
	}))
	defer srv.Close()

	signer := newTestSigner(t)
	c := NewClient(0, 1<<20, 1<<20, true, WithTrustedKeys(signer.source()))
	f := signer.sign(t, "p/c", catalog.FileEntry{URL: srv.URL + "/file", Digest: sha256Prefixed(cat)})
	body, err := c.FetchFile(context.Background(), f)
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
	cat := `{"id":"p/c","resources":[{"id":"r1"}]}`
	mux := http.NewServeMux()
	mux.HandleFunc("/file", func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte(cat)) })
	srv := httptest.NewServer(mux)
	defer srv.Close()
	ctx := context.Background()
	signer := newTestSigner(t)
	keys := WithTrustedKeys(signer.source())

	t.Run("digest mismatch", func(t *testing.T) {
		c := NewClient(5*time.Second, 1<<20, 1<<20, true, keys)
		f := signer.sign(t, "p/c", catalog.FileEntry{URL: srv.URL + "/file", Digest: sha256Prefixed("something else")})
		_, err := c.FetchFile(ctx, f)
		assertPermanentFault(t, err, catalog.FaultDigestMismatch)
	})

	t.Run("artifact over the compressed cap", func(t *testing.T) {
		c := NewClient(5*time.Second, 8, 1<<20, true, keys) // maxBytes=8 < len(cat)
		f := signer.sign(t, "p/c", catalog.FileEntry{URL: srv.URL + "/file", Digest: sha256Prefixed(cat)})
		_, err := c.FetchFile(ctx, f)
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
		f := signer.sign(t, "p/c", catalog.FileEntry{URL: down.URL, Digest: sha256Prefixed(cat)})
		_, err := c.FetchFile(ctx, f)
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
