package fetch

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/beckn-one/beckn-onix/pkg/catalogcrawler/catalog"
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

	c := NewClient(5*time.Second, 1<<20, 1<<20, true) // allowPrivate for httptest (127.0.0.1)
	ctx := context.Background()

	res, err := c.FetchIndex(ctx, srv.URL+"/index", catalog.IndexConditions{})
	if err != nil {
		t.Fatal(err)
	}
	if res.Index.ParticipantID != "p" || res.Index.Version != 7 {
		t.Fatalf("index = %+v, want participantId p version 7", res.Index)
	}

	body, err := c.FetchFile(ctx, catalog.FileEntry{URL: srv.URL + "/file", Digest: sha256Prefixed(cat)})
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != cat {
		t.Fatalf("file body = %q, want %q", body, cat)
	}

	if _, err := c.FetchFile(ctx, catalog.FileEntry{URL: srv.URL + "/file", Digest: "sha-256:deadbeef"}); err == nil {
		t.Fatal("expected digest-mismatch error")
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

	c := NewClient(5*time.Second, 1<<20, 1<<20, true)
	ctx := context.Background()

	// Encoding inferred from the .json.gzip suffix.
	body, err := c.FetchFile(ctx, catalog.FileEntry{URL: srv.URL + "/c.json.gzip", Digest: digest})
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != cat {
		t.Fatalf("suffix-decoded = %q, want %q", body, cat)
	}

	// Encoding taken from the explicit FileEntry.Encoding on a plain URL.
	body, err = c.FetchFile(ctx, catalog.FileEntry{URL: srv.URL + "/c", Encoding: "gzip", Digest: digest})
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
	reject := []string{"http://127.0.0.1/x", "http://10.0.0.1/x", "http://192.168.1.1/x", "ftp://example.com/x"}
	for _, u := range reject {
		if err := checkPublicURL(u); err == nil {
			t.Errorf("checkPublicURL(%q) = nil, want error", u)
		}
	}
	if err := checkPublicURL("https://93.184.216.34/x"); err != nil {
		t.Errorf("checkPublicURL(public IP) = %v, want nil", err)
	}
}
