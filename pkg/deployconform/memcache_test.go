package deployconform

import (
	"context"
	"testing"
	"time"
)

// TestMemCache exercises the in-memory cache: roundtrip, absent keys, TTL
// expiry, non-expiring entries, Delete, and Clear.
func TestMemCache(t *testing.T) {
	ctx := context.Background()

	t.Run("set get roundtrip", func(t *testing.T) {
		c := newMemCache()
		if err := c.Set(ctx, "k", "v", time.Minute); err != nil {
			t.Fatalf("Set() error: %v", err)
		}
		got, err := c.Get(ctx, "k")
		if err != nil {
			t.Fatalf("Get() error: %v", err)
		}
		if got != "v" {
			t.Fatalf("Get() = %q, want %q", got, "v")
		}
	})

	t.Run("absent key returns empty and nil error", func(t *testing.T) {
		c := newMemCache()
		got, err := c.Get(ctx, "missing")
		if err != nil {
			t.Fatalf("Get() error: %v", err)
		}
		if got != "" {
			t.Fatalf("Get() = %q, want empty string", got)
		}
	})

	t.Run("entry expires after ttl", func(t *testing.T) {
		c := newMemCache()
		if err := c.Set(ctx, "k", "v", 10*time.Millisecond); err != nil {
			t.Fatalf("Set() error: %v", err)
		}
		time.Sleep(20 * time.Millisecond)
		got, err := c.Get(ctx, "k")
		if err != nil {
			t.Fatalf("Get() error: %v", err)
		}
		if got != "" {
			t.Fatalf("Get() after expiry = %q, want empty string", got)
		}
	})

	t.Run("non-positive ttl never expires", func(t *testing.T) {
		c := newMemCache()
		for _, ttl := range []time.Duration{0, -time.Second} {
			if err := c.Set(ctx, "k", "v", ttl); err != nil {
				t.Fatalf("Set(ttl=%v) error: %v", ttl, err)
			}
			time.Sleep(5 * time.Millisecond)
			got, err := c.Get(ctx, "k")
			if err != nil {
				t.Fatalf("Get() error: %v", err)
			}
			if got != "v" {
				t.Fatalf("Get() with ttl=%v = %q, want %q", ttl, got, "v")
			}
		}
	})

	t.Run("delete removes key", func(t *testing.T) {
		c := newMemCache()
		if err := c.Set(ctx, "k", "v", 0); err != nil {
			t.Fatalf("Set() error: %v", err)
		}
		if err := c.Delete(ctx, "k"); err != nil {
			t.Fatalf("Delete() error: %v", err)
		}
		if got, _ := c.Get(ctx, "k"); got != "" {
			t.Fatalf("Get() after Delete = %q, want empty string", got)
		}
	})

	t.Run("clear removes every entry", func(t *testing.T) {
		c := newMemCache()
		for _, k := range []string{"a", "b", "c"} {
			if err := c.Set(ctx, k, k+"-value", 0); err != nil {
				t.Fatalf("Set(%q) error: %v", k, err)
			}
		}
		if err := c.Clear(ctx); err != nil {
			t.Fatalf("Clear() error: %v", err)
		}
		for _, k := range []string{"a", "b", "c"} {
			if got, _ := c.Get(ctx, k); got != "" {
				t.Fatalf("Get(%q) after Clear = %q, want empty string", k, got)
			}
		}
	})
}
