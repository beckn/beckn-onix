package store

// provider_test.go — the backend contract and its selection: the compile-time
// proof that Backend (and the Postgres implementation) satisfy the runner's
// Store port, and the factory/registry behavior an operator sees when the
// configured provider name is wrong. None of these tests need a database:
// building the Postgres backend opens a lazy pool, it does not connect.

import (
	"context"
	"strings"
	"testing"

	"github.com/beckn-one/beckn-onix/pkg/plugin/implementation/crawler/internal/crawler/config"
	"github.com/beckn-one/beckn-onix/pkg/plugin/implementation/crawler/internal/crawler/runner"
)

// Backend mirrors runner.Store by hand (this package cannot import runner
// outside tests without creating a test-time cycle), so these assertions are
// what keeps the two in sync: the first fails to compile if the mirror drifts,
// the second if the Postgres backend stops implementing it.
var (
	_ runner.Store = Backend(nil)
	_ Backend      = (*postgresBackend)(nil)
)

// The default provider name in config must name a backend registered here,
// otherwise a crawler with no CRAWLER_STORE_PROVIDER set cannot start.
func TestDefaultStoreProviderIsRegistered(t *testing.T) {
	if config.DefaultStoreProvider != ProviderPostgres {
		t.Fatalf("config.DefaultStoreProvider = %q, want %q", config.DefaultStoreProvider, ProviderPostgres)
	}
	if !backends.IsRegistered(config.DefaultStoreProvider) {
		t.Fatalf("default provider %q not registered (available: %v)", config.DefaultStoreProvider, AvailableBackends())
	}
}

func TestNewBackend(t *testing.T) {
	tests := []struct {
		name     string
		provider string
		cfg      Config
		wantErr  []string // substrings the error must contain ("" entries mean: expect success)
	}{
		{
			name:     "postgres with dsn",
			provider: ProviderPostgres,
			cfg:      Config{DSN: "postgres://u:p@h:5432/db?sslmode=disable"},
		},
		{
			name:     "postgres without dsn",
			provider: ProviderPostgres,
			wantErr:  []string{"postgres", "DSN"},
		},
		{
			name:     "unknown provider names what is available",
			provider: "mysql",
			cfg:      Config{DSN: "x"},
			wantErr:  []string{`unknown provider "mysql"`, ProviderPostgres},
		},
		{
			name:    "empty provider is not guessed",
			cfg:     Config{DSN: "x"},
			wantErr: []string{`unknown provider ""`, ProviderPostgres},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b, err := NewBackend(tt.provider, tt.cfg)
			if len(tt.wantErr) == 0 {
				if err != nil {
					t.Fatalf("NewBackend: %v", err)
				}
				if b == nil {
					t.Fatal("NewBackend returned a nil backend")
				}
				if err := b.Close(); err != nil {
					t.Fatalf("Close: %v", err)
				}
				return
			}
			if err == nil {
				_ = b.Close()
				t.Fatal("expected an error")
			}
			for _, want := range tt.wantErr {
				if !strings.Contains(err.Error(), want) {
					t.Fatalf("error %q missing %q", err, want)
				}
			}
		})
	}
}

func TestAvailableBackends(t *testing.T) {
	got := AvailableBackends()
	found := false
	for _, name := range got {
		if name == ProviderPostgres {
			found = true
		}
	}
	if !found {
		t.Fatalf("AvailableBackends() = %v, want it to contain %q", got, ProviderPostgres)
	}
}

// fakeBackend is a registry stand-in: the registry is generic, so its behavior
// is tested without a real backend.
type fakeBackend struct{ Backend }

func TestRegistry(t *testing.T) {
	tests := []struct {
		name       string
		register   string
		create     string
		buildErr   error
		wantErr    string
		wantCreate bool
	}{
		{name: "creates a registered provider", register: "fake", create: "fake", wantCreate: true},
		{name: "rejects an unregistered name", register: "fake", create: "other", wantErr: `unknown provider "other" (available: fake)`},
		{name: "wraps a builder failure", register: "fake", create: "fake", buildErr: context.Canceled, wantErr: `building provider "fake"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := NewRegistry[Config, Backend]()
			r.Register(tt.register, func(Config) (Backend, error) {
				if tt.buildErr != nil {
					return nil, tt.buildErr
				}
				return fakeBackend{}, nil
			})
			if !r.IsRegistered(tt.register) {
				t.Fatalf("IsRegistered(%q) = false", tt.register)
			}
			got, err := r.Create(tt.create, Config{})
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("Create error = %v, want it to contain %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("Create: %v", err)
			}
			if tt.wantCreate && got == nil {
				t.Fatal("Create returned a nil provider")
			}
		})
	}
}
