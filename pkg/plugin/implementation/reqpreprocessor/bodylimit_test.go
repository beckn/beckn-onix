package reqpreprocessor

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestNewPreProcessor_BodyOverLimit(t *testing.T) {
	middleware, err := NewPreProcessor(&Config{Role: "bpp"})
	if err != nil {
		t.Fatalf("NewPreProcessor() error = %v", err)
	}
	called := false
	h := middleware(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called = true }))
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/bpp/receiver/discover", strings.NewReader(strings.Repeat("a", 64)))
	req.Body = http.MaxBytesReader(rr, req.Body, 16)

	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("status = %d, want 413", rr.Code)
	}
	if called {
		t.Error("the next handler ran for a body over the limit")
	}
}
