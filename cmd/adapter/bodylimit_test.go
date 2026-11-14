package main

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/beckn-one/beckn-onix/core/module"
)

// chunked hides the length of r so the request carries no Content-Length,
// as a chunked upload does.
type chunked struct{ io.Reader }

func TestLimitRequestBody(t *testing.T) {
	const limit = 16

	t.Run("declared length over the limit is refused unread", func(t *testing.T) {
		called := false
		h := limitRequestBody(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called = true }), limit)
		req := httptest.NewRequest(http.MethodPost, "/bpp/receiver/discover", strings.NewReader(strings.Repeat("a", limit+1)))
		rr := httptest.NewRecorder()

		h.ServeHTTP(rr, req)

		if rr.Code != http.StatusRequestEntityTooLarge {
			t.Errorf("status = %d, want 413", rr.Code)
		}
		if called {
			t.Error("the handler ran for a request over the limit")
		}
		if ct := rr.Header().Get("Content-Type"); ct != "application/json" {
			t.Errorf("Content-Type = %q, want application/json", ct)
		}
	})

	t.Run("undeclared length over the limit fails the read", func(t *testing.T) {
		var readErr error
		h := limitRequestBody(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
			_, readErr = io.ReadAll(r.Body)
		}), limit)
		req := httptest.NewRequest(http.MethodPost, "/bpp/receiver/discover", chunked{strings.NewReader(strings.Repeat("a", limit+1))})
		if req.ContentLength > 0 {
			t.Fatalf("test setup: ContentLength = %d, want it unknown", req.ContentLength)
		}

		h.ServeHTTP(httptest.NewRecorder(), req)

		var tooLarge *http.MaxBytesError
		if !errors.As(readErr, &tooLarge) {
			t.Errorf("read error = %v, want *http.MaxBytesError", readErr)
		}
	})

	t.Run("body at the limit passes through intact", func(t *testing.T) {
		want := strings.Repeat("a", limit)
		var got []byte
		h := limitRequestBody(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
			got, _ = io.ReadAll(r.Body)
		}), limit)
		rr := httptest.NewRecorder()

		h.ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/bpp/receiver/discover", strings.NewReader(want)))

		if rr.Code != http.StatusOK || string(got) != want {
			t.Errorf("status = %d, body = %q; want 200 and the body unchanged", rr.Code, got)
		}
	})
}

func TestMaxBodyBytesDefault(t *testing.T) {
	for _, configured := range []int64{0, -1} {
		if got := (httpConfig{MaxBodyBytes: configured}).maxBodyBytes(); got != defaultMaxBodyBytes {
			t.Errorf("maxBodyBytes() with %d configured = %d, want the default %d", configured, got, defaultMaxBodyBytes)
		}
	}
	if got := (httpConfig{MaxBodyBytes: 2048}).maxBodyBytes(); got != 2048 {
		t.Errorf("maxBodyBytes() = %d, want 2048", got)
	}
}

// The limit must sit in front of every route the server exposes, so it is
// asserted on the handler newServer returns and not on a single module.
func TestNewServerLimitsRequestBody(t *testing.T) {
	cfg := &Config{Modules: []module.Config{}, HTTP: httpConfig{Port: "8080", MaxBodyBytes: 16}}
	srv, err := newServer(context.Background(), new(MockPluginManager), cfg)
	if err != nil {
		t.Fatalf("newServer() error = %v", err)
	}
	rr := httptest.NewRecorder()

	srv.ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/any/route", strings.NewReader(strings.Repeat("a", 17))))

	if rr.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("status = %d, want 413", rr.Code)
	}
}
