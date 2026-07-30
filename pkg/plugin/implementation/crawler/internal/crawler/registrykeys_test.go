package crawler

// registrykeys_test.go — the composition root's trust-anchor gate. Catalog file
// signatures are verified against the publisher's key AS HELD IN THE NETWORK
// REGISTRY, so the registry is the crawler's only trust anchor. The fetch
// client's signature check fails closed, which means a crawler built without a
// registry parks every catalog it ever sees. New must refuse to build one,
// loudly, before it opens a database. A disabled crawler still needs nothing.

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/beckn-one/beckn-onix/pkg/model"
	"github.com/beckn-one/beckn-onix/pkg/plugin/implementation/crawler/internal/crawler/catalog"
	"github.com/beckn-one/beckn-onix/pkg/plugin/implementation/crawler/internal/crawler/config"
	"github.com/beckn-one/beckn-onix/pkg/plugin/implementation/crawler/internal/crawler/store"
)

// fakeBackendName is the store provider these tests register, so New can be
// driven to completion without a database. It is deliberately not a name any
// operator would configure.
const fakeBackendName = "fake-backend-for-registry-test"

func init() {
	store.RegisterBackend(fakeBackendName, func(store.Config) (store.Backend, error) {
		return fakeBackend{}, nil
	})
}

// fakeBackend satisfies store.Backend with do-nothing methods: these tests
// exercise construction, not persistence.
type fakeBackend struct{}

func (fakeBackend) GetCatalogVersion(context.Context, string) (int64, bool, error) {
	return 0, false, nil
}
func (fakeBackend) UpsertCatalog(context.Context, catalog.CatalogState) error { return nil }
func (fakeBackend) CountParked(context.Context) (int, error)                  { return 0, nil }
func (fakeBackend) CountTracked(context.Context) (int, error)                 { return 0, nil }
func (fakeBackend) GetCatalogReports(context.Context, string) ([]catalog.PassReport, error) {
	return nil, nil
}
func (fakeBackend) RecordFailure(context.Context, string, string, string, catalog.PassReport) error {
	return nil
}
func (fakeBackend) GetIndex(context.Context, string) (*catalog.IndexState, error) { return nil, nil }
func (fakeBackend) KnownIndexes(context.Context) ([]catalog.KnownIndex, error)    { return nil, nil }
func (fakeBackend) UpsertIndex(context.Context, string, string, string, int64, string, time.Time, string, string) error {
	return nil
}
func (fakeBackend) AdvanceIndexCadence(context.Context, string, time.Time) error { return nil }
func (fakeBackend) Enqueue(context.Context, catalog.QueueItem) error             { return nil }
func (fakeBackend) ClaimNext(context.Context) (*catalog.ClaimedItem, error)      { return nil, nil }
func (fakeBackend) RescheduleQueueItem(context.Context, string, string, time.Time) error {
	return nil
}
func (fakeBackend) ParkQueueItem(context.Context, string, string) error { return nil }
func (fakeBackend) Complete(context.Context, string, string, int64, catalog.CatalogState) error {
	return nil
}
func (fakeBackend) QueueDepth(context.Context) (int, error) { return 0, nil }
func (fakeBackend) Migrate(context.Context) error           { return nil }
func (fakeBackend) Close() error                            { return nil }

// stubRegistry is an inert fetch.KeyRegistry: construction only needs the
// dependency to be present, never to answer.
type stubRegistry struct{}

func (stubRegistry) Lookup(context.Context, *model.Subscription) ([]model.Subscription, error) {
	return nil, nil
}

func TestNewRequiresRegistry(t *testing.T) {
	enabled := func() config.Settings {
		return config.Settings{
			Enabled:       true,
			StoreProvider: fakeBackendName,
			DBDSN:         "fake://ignored",
			PushEndpoint:  "https://d/push",
			IndexURLs:     []string{"https://a/i"},
		}
	}

	tests := []struct {
		name string
		// mutate adjusts an otherwise valid enabled Settings.
		mutate func(*config.Settings)
		// opts builds the Options New is called with.
		opts    func() Options
		wantErr error  // errors.Is target; nil means no error expected
		wantIn  string // substring the error message must contain
	}{
		{
			name:    "enabled with no registry refuses to start",
			mutate:  func(*config.Settings) {},
			opts:    func() Options { return Options{} },
			wantErr: ErrNoRegistry,
			wantIn:  "registry plugin is required",
		},
		{
			name:   "enabled with a registry constructs",
			mutate: func(*config.Settings) {},
			opts:   func() Options { return Options{Registry: stubRegistry{}} },
		},
		{
			name: "a registry-only source is refused, not silently ignored",
			mutate: func(s *config.Settings) {
				s.IndexURLs = nil
				s.RegistryURL = "https://registry/"
			},
			opts:   func() Options { return Options{Registry: stubRegistry{}} },
			wantIn: "registry source is not wired",
		},
		{
			name:   "disabled needs no registry at all",
			mutate: func(s *config.Settings) { *s = config.Settings{} },
			opts:   func() Options { return Options{} },
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := enabled()
			tt.mutate(&s)
			ctx := context.Background()
			c, closer, err := New(ctx, s, tt.opts())
			if tt.wantErr != nil || tt.wantIn != "" {
				if err == nil {
					if closer != nil {
						_ = closer()
					}
					t.Fatal("expected New to fail")
				}
				if tt.wantErr != nil && !errors.Is(err, tt.wantErr) {
					t.Fatalf("error = %v, want errors.Is %v", err, tt.wantErr)
				}
				if tt.wantIn != "" && !strings.Contains(err.Error(), tt.wantIn) {
					t.Fatalf("error %q does not contain %q", err.Error(), tt.wantIn)
				}
				if c != nil || closer != nil {
					t.Fatal("a failed New must return no crawler and no closer")
				}
				return
			}
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			if c == nil || closer == nil {
				t.Fatal("New returned a nil crawler or closer")
			}
			if err := closer(); err != nil {
				t.Fatalf("closer: %v", err)
			}
		})
	}
}

// The registry gate must run BEFORE the database is opened or migrated. A
// crawler that cannot verify anything should not have touched Postgres.
func TestNewChecksRegistryBeforeOpeningTheStore(t *testing.T) {
	_, _, err := New(context.Background(), config.Settings{
		Enabled:       true,
		StoreProvider: "nosuchdb", // would fail loudly if the store were reached first
		DBDSN:         "postgres://u:p@h/db",
		PushEndpoint:  "https://d/push",
		IndexURLs:     []string{"https://a/i"},
	}, Options{})
	if !errors.Is(err, ErrNoRegistry) {
		t.Fatalf("error = %v, want ErrNoRegistry (the trust anchor is checked first)", err)
	}
}

// TestErrNoRegistryMessage pins the operator-facing wording: the message has to
// say what to configure and why, because it is the only signal an operator gets
// before the crawler would otherwise park everything.
func TestErrNoRegistryMessage(t *testing.T) {
	msg := ErrNoRegistry.Error()
	for _, want := range []string{
		"registry plugin is required",
		"crawler is enabled",
		"publisher's registry key",
	} {
		if !strings.Contains(msg, want) {
			t.Fatalf("ErrNoRegistry %q does not contain %q", msg, want)
		}
	}
}
