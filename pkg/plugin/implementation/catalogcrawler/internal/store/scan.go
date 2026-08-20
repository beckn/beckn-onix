package store

// scan.go — small helpers that map Go zero values to SQL NULL so the
// insert/upsert statements store NULL instead of empty strings / zeroes.

import "time"

func nullStr(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func nullTime(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return t
}

func nullBytes(b []byte) any {
	if len(b) == 0 {
		return nil
	}
	return string(b)
}
