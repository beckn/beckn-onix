package handler

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/beckn-one/beckn-onix/pkg/model"
)

// A body cut off by the server's size limit is the sender's fault, so the
// handler answers 413 and not the 500 it gives for other read failures.
func TestServeHTTP_BodyOverLimit(t *testing.T) {
	h := &stdHandler{
		SubscriberID: "test-sub",
		role:         model.RoleBPP,
		moduleName:   "test-module",
	}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/bpp/receiver/discover", strings.NewReader(strings.Repeat("a", 64)))
	req.Body = http.MaxBytesReader(rr, req.Body, 16)

	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("status = %d, want 413", rr.Code)
	}
}
