package catalogpublisher

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/beckn-one/beckn-onix/core/module/handler"
)

func TestReadPublishBodyOverLimit(t *testing.T) {
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/catalog/publish", strings.NewReader(strings.Repeat("a", 64)))
	req.Body = http.MaxBytesReader(rr, req.Body, 16)

	_, err := readPublishBody(req)

	var statusErr *handler.StatusError
	if !errors.As(err, &statusErr) || statusErr.Status != http.StatusRequestEntityTooLarge {
		t.Errorf("err = %v, want a StatusError with 413", err)
	}
}

func TestReadPublishBodyUnderLimit(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/catalog/publish", strings.NewReader(`{"catalogs":[]}`))
	body, err := readPublishBody(req)
	if err != nil || string(body) != `{"catalogs":[]}` {
		t.Errorf("body = %q, err = %v", body, err)
	}
}
