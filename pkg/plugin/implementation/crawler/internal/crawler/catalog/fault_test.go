package catalog

// fault_test.go — the park-vs-retry classifier. The class a PermanentError
// carries is what an operator reads in the fault log and the
// crawler_sync_outcome_total label, so it has to survive wrapping and it has to
// not collapse every permanent failure into "decode".

import (
	"errors"
	"fmt"
	"net/url"
	"testing"
)

func TestClassifyFault(t *testing.T) {
	tests := []struct {
		name       string
		httpStatus int
		err        error
		want       FaultClass
	}{
		// A permanent error that named its class reports that class — not decode.
		{"digest mismatch keeps its class", 0,
			PermanentFaultf(FaultDigestMismatch, "digest mismatch"), FaultDigestMismatch},
		{"ssrf keeps its class", 0,
			PermanentFaultf(FaultSSRF, "refusing private host"), FaultSSRF},
		{"oversize keeps its class", 0,
			PermanentFaultf(FaultOversize, "too big"), FaultOversize},
		{"continuity gap keeps its class", 0,
			PermanentFaultf(FaultGap, "gap in change files"), FaultGap},

		// Unclassified permanents still fall back to decode (existing behaviour).
		{"unclassified permanent falls back to decode", 0,
			Permanentf("unsupported encoding"), FaultDecode},

		// The class must survive wrapping: fetch errors reach ClassifyFault through
		// fmt.Errorf(%w) and, for dial-time SSRF rejections, through *url.Error.
		{"class survives fmt.Errorf wrapping", 0,
			fmt.Errorf("fetching: %w", PermanentFaultf(FaultGap, "gap")), FaultGap},
		{"class survives url.Error wrapping", 0,
			&url.Error{Op: "Get", URL: "https://x", Err: PermanentFaultf(FaultSSRF, "refusing private address")},
			FaultSSRF},

		// Non-permanent stays retryable.
		{"plain error is transient", 0, errors.New("connection reset"), FaultTransient},
		{"no error is transient", 0, nil, FaultTransient},
		{"5xx is transient", 503, nil, FaultTransient},

		// Push-status rules are unchanged by the class plumbing.
		{"4xx is a push rejection", 400, nil, FaultPushRejected},
		{"408 is retryable", 408, nil, FaultTransient},
		{"425 is retryable", 425, nil, FaultTransient},
		{"429 is retryable", 429, nil, FaultTransient},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := ClassifyFault(tc.httpStatus, tc.err); got != tc.want {
				t.Fatalf("ClassifyFault(%d, %v) = %q, want %q", tc.httpStatus, tc.err, got, tc.want)
			}
		})
	}
}

// Every class the fetch/decode/resolve layers now raise must be Permanent(), or
// the runner would classify it correctly and then still retry it forever.
func TestRaisedFaultClassesArePermanent(t *testing.T) {
	for _, fc := range []FaultClass{FaultDigestMismatch, FaultSSRF, FaultOversize, FaultGap, FaultDecode} {
		if !fc.Permanent() {
			t.Errorf("FaultClass %q is raised as permanent but Permanent() = false", fc)
		}
	}
}

func TestPermanentClass(t *testing.T) {
	if got := PermanentClass(PermanentFaultf(FaultGap, "x")); got != FaultGap {
		t.Errorf("PermanentClass(classified) = %q, want %q", got, FaultGap)
	}
	if got := PermanentClass(Permanentf("x")); got != "" {
		t.Errorf("PermanentClass(unclassified) = %q, want empty", got)
	}
	if got := PermanentClass(errors.New("x")); got != "" {
		t.Errorf("PermanentClass(transient) = %q, want empty", got)
	}
}
