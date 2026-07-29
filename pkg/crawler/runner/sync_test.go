package runner

import (
	"errors"
	"testing"

	"github.com/beckn-one/beckn-onix/pkg/crawler/catalog"
)

// classifyOutcome is the ONE place the push-outcome rule lives. External
// behavior (retry-vs-park, cursor advancement) is decided elsewhere; this only
// pins the persisted SyncOutcome string.
func TestClassifyOutcome(t *testing.T) {
	tests := []struct {
		name       string
		httpStatus int
		acked      int
		err        error
		want       catalog.SyncOutcome
	}{
		{"4xx rejection -> faulted", 400, 0, nil, catalog.OutcomeFaulted},
		{"4xx even with acked -> faulted", 409, 2, nil, catalog.OutcomeFaulted},
		{"transport error -> faulted", 0, 0, errors.New("boom"), catalog.OutcomeFaulted},
		{"5xx none acked -> faulted", 500, 0, nil, catalog.OutcomeFaulted},
		{"5xx some acked -> partial", 500, 1, nil, catalog.OutcomePartial},
		{"pre-push failure (status 0) -> faulted", 0, 0, nil, catalog.OutcomeFaulted},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := classifyOutcome(tt.httpStatus, tt.acked, tt.err); got != tt.want {
				t.Errorf("classifyOutcome(%d,%d,%v) = %q, want %q", tt.httpStatus, tt.acked, tt.err, got, tt.want)
			}
		})
	}
}

// stepPhrase maps each fault class to the "couldn't <…>" clause the failure
// message is built from (docs/crawler-logs.md §4).
func TestStepPhrase(t *testing.T) {
	cases := map[string]string{
		"index_fetch":     "resolve the catalog",
		"absent":          "resolve the catalog",
		"decode":          "unpack the files",
		"gap":             "unpack the files",
		"digest_mismatch": "verify the downloaded files",
		"oversize":        "batch the catalog",
		"push_schema":     "send the catalog to Discovery",
		"push_rejected":   "send the catalog to Discovery",
		"transient":       "send the catalog to Discovery",
		"store":           "save progress",
	}
	for fault, want := range cases {
		if got := stepPhrase(fault); got != want {
			t.Errorf("stepPhrase(%q) = %q, want %q", fault, got, want)
		}
	}
}
