package catalogcrawler

import "time"

// sync_status values on crawler_index (matches the design's CHECK
// constraint): initial, every part landed, some landed, or none.
const (
	SyncPending = "pending"
	SyncOK      = "ok"
	SyncPartial = "partial"
	SyncFailed  = "failed"
)

// backoffBase and backoffCap bound the retry schedule.
const (
	backoffBase = time.Second
	backoffCap  = 5 * time.Minute
)

// Backoff returns the delay before retry number `attempts` (0-based):
// capped exponential (base * 2^attempts, ceilinged at backoffCap).
func Backoff(attempts int) time.Duration {
	if attempts < 0 {
		attempts = 0
	}
	// Cap the shift so base<<attempts can't overflow past the ceiling.
	if attempts > 62 {
		return backoffCap
	}
	d := backoffBase << uint(attempts)
	if d <= 0 || d > backoffCap {
		return backoffCap
	}
	return d
}

// PartOutcome is the result of pushing one part (batch) of a catalog.
type PartOutcome struct {
	Acked      bool
	HTTPStatus int
	Reason     string
}

// Rollup collapses per-part push outcomes into a catalog-level sync_status
// and returns the parts that failed (for retry + the error reason).
func Rollup(outcomes []PartOutcome) (status string, failed []PartOutcome) {
	acked := 0
	for _, o := range outcomes {
		if o.Acked {
			acked++
		} else {
			failed = append(failed, o)
		}
	}
	switch {
	case len(failed) == 0:
		return SyncOK, nil
	case acked == 0:
		return SyncFailed, failed
	default:
		return SyncPartial, failed
	}
}
