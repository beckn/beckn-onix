package crawler

// disabled_test.go — the opt-in composition root: a disabled crawler must
// construct with no DSN and no database, start and stop as a no-op, and say why
// an on-demand crawl did nothing.

import (
	"context"
	"errors"
	"testing"

	"github.com/beckn-one/beckn-onix/pkg/plugin/implementation/crawler/internal/crawler/config"
)

func TestNewDisabled(t *testing.T) {
	tests := []struct {
		name string
		s    config.Settings
	}{
		{name: "zero settings", s: config.Settings{}},
		{name: "disabled with a store provider but no dsn", s: config.Settings{StoreProvider: config.DefaultStoreProvider}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			c, closer, err := New(ctx, tt.s, Options{})
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			if c == nil || closer == nil {
				t.Fatal("New returned a nil crawler or closer")
			}
			if err := c.Start(ctx); err != nil {
				t.Fatalf("Start: %v", err)
			}
			if _, err := c.CrawlNow(ctx, "https://x/index.json"); !errors.Is(err, ErrDisabled) {
				t.Fatalf("CrawlNow error = %v, want %v", err, ErrDisabled)
			}
			if err := c.Stop(); err != nil {
				t.Fatalf("Stop: %v", err)
			}
			if err := closer(); err != nil {
				t.Fatalf("closer: %v", err)
			}
		})
	}
}

func TestNewEnabledRejectsUnknownStoreProvider(t *testing.T) {
	_, _, err := New(context.Background(), config.Settings{
		Enabled:       true,
		StoreProvider: "nosuchdb",
		DBDSN:         "postgres://u:p@h/db",
	}, Options{})
	if err == nil {
		t.Fatal("expected an error for an unknown store provider")
	}
}
