package store

// postgres.go — the Postgres backend: it wraps the pgx-backed Store with the
// pool lifecycle (migrate + close) and registers itself under "postgres". This
// is the only file in the crawler that ties the persistence layer to a driver.

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
)

// ProviderPostgres is the registered name of the Postgres backend.
const ProviderPostgres = "postgres"

func init() { RegisterBackend(ProviderPostgres, newPostgresBackend) }

// postgresBackend owns the pool it opened, so Migrate and Close belong to the
// backend and no driver handle escapes this package. The embedded *Store
// supplies the state methods.
type postgresBackend struct {
	*Store
	db *sql.DB
}

// newPostgresBackend opens a pgx pool for cfg.DSN. sql.Open does not connect, so
// an unreachable host surfaces on Migrate rather than here.
func newPostgresBackend(cfg Config) (Backend, error) {
	dsn := strings.TrimSpace(cfg.DSN)
	if dsn == "" {
		return nil, fmt.Errorf("a DSN is required (CRAWLER_DB_DSN)")
	}
	db, err := Open(dsn)
	if err != nil {
		return nil, fmt.Errorf("opening db: %w", err)
	}
	return &postgresBackend{Store: New(db), db: db}, nil
}

// Migrate applies the embedded migrations (idempotent).
func (p *postgresBackend) Migrate(ctx context.Context) error { return Migrate(ctx, p.db) }

// Close closes the pool.
func (p *postgresBackend) Close() error { return p.db.Close() }
