// Package testutil holds small assertion helpers shared across this repo's
// test files. It is a regular (non-_test.go) package so it can be imported
// from other packages' tests, unlike a _test.go file which Go restricts to
// its own package.
package testutil

import (
	"errors"
	"testing"

	"github.com/beckn-one/beckn-onix/pkg/model"
)

// RequireBadReqCode asserts that errors.As finds a *model.BadReqErr in err
// and that its BecknError().Code equals wantCode. Shared by every plugin test
// suite that classifies failures onto *model.BadReqErr (reqmapper, router,
// reqpreprocessor, encrypter, decrypter, ...) instead of each reimplementing
// the same assertion locally.
func RequireBadReqCode(t *testing.T, err error, wantCode string) {
	t.Helper()

	var badReqErr *model.BadReqErr
	if !errors.As(err, &badReqErr) {
		t.Fatalf("expected errors.As to find a *model.BadReqErr in %v (%T)", err, err)
	}
	if code := badReqErr.BecknError().Code; code != wantCode {
		t.Errorf("BecknError().Code = %s, want %s", code, wantCode)
	}
}
