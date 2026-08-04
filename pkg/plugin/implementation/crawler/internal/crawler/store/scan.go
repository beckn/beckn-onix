package store

// scan.go — small helpers that map Go zero values to SQL NULL so the
// insert/upsert statements store NULL instead of empty strings / zeroes.

import "time"

// null helpers: map Go zero values to SQL NULL.

func nullStr(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func nullIntZero(n int) any {
	if n == 0 {
		return nil
	}
	return n
}

func nullInt64Zero(n int64) any {
	if n == 0 {
		return nil
	}
	return n
}

func nullTime(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return t
}
