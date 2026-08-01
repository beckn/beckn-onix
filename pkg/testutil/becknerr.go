// Package testutil holds small assertion helpers shared across this repo's
// test files. It is a regular (non-_test.go) package so it can be imported
// from other packages' tests, unlike a _test.go file which Go restricts to
// its own package.
package testutil

import (
	"errors"
	"net/http"
	"testing"

	"github.com/beckn-one/beckn-onix/pkg/model"
)

// RequireBadReqCode asserts that errors.As finds a *model.CodedErr in err,
// that its status is 400, and that its BecknError().Code equals wantCode.
// Shared by every plugin test suite that classifies failures as bad requests
// (reqmapper, router, reqpreprocessor, encrypter, decrypter, ...) instead of
// each reimplementing the same assertion locally.
//
// The status check carries the bad-request half of the assertion: one
// CodedErr type covers 400, 401 and 404, so the type alone proves nothing.
func RequireBadReqCode(t *testing.T, err error, wantCode string) {
	t.Helper()

	var codedErr *model.CodedErr
	if !errors.As(err, &codedErr) {
		t.Fatalf("expected errors.As to find a *model.CodedErr in %v (%T)", err, err)
	}
	if status := codedErr.HTTPStatus(); status != http.StatusBadRequest {
		t.Errorf("HTTPStatus() = %d, want %d", status, http.StatusBadRequest)
	}
	if code := codedErr.BecknError().Code; code != wantCode {
		t.Errorf("BecknError().Code = %s, want %s", code, wantCode)
	}
}
