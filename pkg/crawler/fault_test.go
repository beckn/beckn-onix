package crawler

import "testing"

// assertPermanentFault is the shared test helper: asserts err is a
// PermanentError of the given class, and that the class itself reports
// Permanent() (so a caller correctly parks/rejects rather than retries it).
func assertPermanentFault(t *testing.T, err error, want FaultClass) {
	t.Helper()
	if err == nil {
		t.Fatalf("want a %q error, got nil", want)
	}
	if !IsPermanent(err) {
		t.Fatalf("%v: must be permanent (give up), not transient (retry forever)", err)
	}
	if got := ClassifyFault(0, err); got != want {
		t.Fatalf("ClassifyFault(%v) = %q, want %q", err, got, want)
	}
	if !want.Permanent() {
		t.Fatalf("FaultClass %q must be Permanent() for a caller to give up on it", want)
	}
}
