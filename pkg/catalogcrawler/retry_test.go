package catalogcrawler

import (
	"testing"
	"time"
)

func TestBackoff(t *testing.T) {
	tests := []struct {
		attempts int
		want     time.Duration
	}{
		{0, 1 * time.Second},
		{1, 2 * time.Second},
		{2, 4 * time.Second},
		{3, 8 * time.Second},
		{20, 5 * time.Minute},  // ceilinged
		{100, 5 * time.Minute}, // no overflow past the cap
	}
	for _, tt := range tests {
		if got := Backoff(tt.attempts); got != tt.want {
			t.Errorf("Backoff(%d) = %v, want %v", tt.attempts, got, tt.want)
		}
	}
}

func TestRollup(t *testing.T) {
	ok := PartOutcome{Acked: true, HTTPStatus: 200}
	bad := PartOutcome{Acked: false, HTTPStatus: 400, Reason: "schema"}

	tests := []struct {
		name       string
		outcomes   []PartOutcome
		wantStatus string
		wantFailed int
	}{
		{"all acked -> success", []PartOutcome{ok, ok}, SyncOK, 0},
		{"some acked -> partial", []PartOutcome{ok, bad}, SyncPartial, 1},
		{"none acked -> failed", []PartOutcome{bad, bad}, SyncFailed, 2},
		{"empty -> success", nil, SyncOK, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			status, failed := Rollup(tt.outcomes)
			if status != tt.wantStatus {
				t.Errorf("status = %q, want %q", status, tt.wantStatus)
			}
			if len(failed) != tt.wantFailed {
				t.Errorf("failed count = %d, want %d", len(failed), tt.wantFailed)
			}
		})
	}
}
