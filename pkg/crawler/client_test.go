package crawler

// client_test.go — covers the retrieval client end-to-end against httptest
// servers: a plain GET, conditional-GET (200-then-304) round-trips, the size
// cap, and the SSRF guard. Signature/digest/decode are exercised by
// verify_test.go and decode/decode_test.go respectively -- this file only
// covers what Client itself owns: the HTTP mechanics.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestClient_Get(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte("hello"))
	}))
	defer srv.Close()

	c := NewClient(5*time.Second, 1<<20, true)
	body, err := c.Get(context.Background(), srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "hello" {
		t.Fatalf("body = %q, want %q", body, "hello")
	}
}

func TestClient_GetConditional_NotModified(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("If-None-Match") == `"v1"` {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		w.Header().Set("ETag", `"v1"`)
		w.Write([]byte("body"))
	}))
	defer srv.Close()

	c := NewClient(5*time.Second, 1<<20, true)
	ctx := context.Background()

	first, err := c.GetConditional(ctx, srv.URL, Conditions{})
	if err != nil {
		t.Fatal(err)
	}
	if first.NotModified || first.ETag != `"v1"` {
		t.Fatalf("first = %+v, want a fresh 200 with ETag v1", first)
	}

	second, err := c.GetConditional(ctx, srv.URL, Conditions{ETag: first.ETag})
	if err != nil {
		t.Fatal(err)
	}
	if !second.NotModified {
		t.Fatalf("second = %+v, want NotModified given a matching If-None-Match", second)
	}
}

func TestClient_Get_OversizeIsPermanent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write(make([]byte, 100))
	}))
	defer srv.Close()

	c := NewClient(5*time.Second, 10, true)
	_, err := c.Get(context.Background(), srv.URL)
	assertPermanentFault(t, err, FaultOversize)
}

func TestClient_Get_RefusesPrivateHost(t *testing.T) {
	c := NewClient(5*time.Second, 1<<20, false) // allowPrivate=false: production posture
	_, err := c.Get(context.Background(), "http://127.0.0.1:1/x")
	assertPermanentFault(t, err, FaultSSRF)
}

func TestClient_Get_NonHTTPStatusIsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	c := NewClient(5*time.Second, 1<<20, true)
	_, err := c.Get(context.Background(), srv.URL)
	if err == nil {
		t.Fatal("expected an error for a 503")
	}
	if IsPermanent(err) {
		t.Fatalf("a 503 must stay transient (retryable), got permanent: %v", err)
	}
}
