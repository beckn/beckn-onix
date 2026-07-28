// Package store is the crawler's Postgres layer: embedded idempotent migrations
// for the three Phase-1 tables (crawler_index, crawler_queue, crawler_catalog)
// plus a thin Store over database/sql (pgx driver). It is ONE Go package so the
// catalog cursor and the queue can share a transaction in Complete.
package store

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"sort"

	_ "github.com/jackc/pgx/v5/stdlib" // pgx database/sql driver ("pgx")
)

// passHistoryCap bounds how many recent pass records crawler_catalog.push_status
// keeps per catalog (append-and-trim), so a long-running crawler can't bloat the
// row while still giving a useful recent history.
const passHistoryCap = 20

//go:embed migrations/*.sql
var migrationFS embed.FS

// Store is the crawler's persistence API.
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
