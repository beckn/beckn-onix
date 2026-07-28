package runner

import "time"

// backoffBase and backoffCap bound the retry schedule.
const (
	backoffBase = time.Second
	backoffCap  = 5 * time.Minute
)

// Backoff returns the delay before retry number `attempts` (0-based): capped
// exponential (base * 2^attempts, ceilinged at backoffCap).
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
