// Package store is catalogcrawler's Postgres-backed crawlmanager.Store: the
// crawler_index/crawler_queue/crawler_catalog schema and queries ported from
// the catalog-crawler prototype's own store package (working, reused as-is --
// see migrations/). Only the Go surface is new, adapted to crawlmanager's
// leaner Store interface: this plugin doesn't track per-catalog push-outcome
// history or a retire envelope the way the prototype did, so several columns
// that schema still carries (push_status detail, descriptor/provider,
// index cadence) are left unused rather than driving a schema rewrite.
package store

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"sort"

	_ "github.com/jackc/pgx/v5/stdlib" // pgx database/sql driver ("pgx")
)

//go:embed migrations/*.sql
var migrationFS embed.FS

// Store is catalogcrawler's persistence API, implementing crawlmanager.Store.
type Store struct{ db *sql.DB }

// New wraps a *sql.DB.
func New(db *sql.DB) *Store { return &Store{db: db} }

// Open opens a pgx-backed *sql.DB for dsn (no connection is made yet).
func Open(dsn string) (*sql.DB, error) {
	return sql.Open("pgx", dsn)
}

// Migrate applies the embedded migrations in filename order. Each is
// idempotent, so this is safe to run on every startup.
func Migrate(ctx context.Context, db *sql.DB) error {
	entries, err := migrationFS.ReadDir("migrations")
	if err != nil {
		return fmt.Errorf("store: reading migrations: %w", err)
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	for _, n := range names {
		b, err := migrationFS.ReadFile("migrations/" + n)
		if err != nil {
			return fmt.Errorf("store: reading %s: %w", n, err)
		}
		if _, err := db.ExecContext(ctx, string(b)); err != nil {
			return fmt.Errorf("store: applying %s: %w", n, err)
		}
	}
	return nil
}

// execer is satisfied by both *sql.DB and *sql.Tx, so upsertCatalog can run
// standalone or inside Complete's transaction.
type execer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}
