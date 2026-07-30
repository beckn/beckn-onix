package runner

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
