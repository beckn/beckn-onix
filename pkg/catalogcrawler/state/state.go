// Package state is the crawler's Postgres layer: embedded idempotent
// migrations for the three Phase-1 tables (crawler_index, crawler_queue,
// crawler_catalog) plus a thin Store over database/sql (pgx driver).
package state

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"sort"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib" // pgx database/sql driver ("pgx")
)

//go:embed migrations/*.sql
var migrationFS embed.FS

// Open opens a pgx-backed *sql.DB for dsn (no connection is made yet).
func Open(dsn string) (*sql.DB, error) {
	return sql.Open("pgx", dsn)
}

// Migrate applies the embedded migrations in filename order. Each is
// idempotent (IF NOT EXISTS), so this is safe to run on every startup.
func Migrate(ctx context.Context, db *sql.DB) error {
	entries, err := migrationFS.ReadDir("migrations")
	if err != nil {
		return fmt.Errorf("state: reading migrations: %w", err)
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
			return fmt.Errorf("state: reading %s: %w", n, err)
		}
		if _, err := db.ExecContext(ctx, string(b)); err != nil {
			return fmt.Errorf("state: applying %s: %w", n, err)
		}
	}
	return nil
}

// Store is the crawler's persistence API.
type Store struct{ db *sql.DB }

// New wraps a *sql.DB.
func New(db *sql.DB) *Store { return &Store{db: db} }

// IndexState is the stored state for one index (the change gate + cadence).
type IndexState struct {
	IndexVersion int64
	SyncStatus   string
	NextCrawlAt  time.Time
}

// GetIndex returns the stored state for an index, or nil if never crawled.
func (s *Store) GetIndex(ctx context.Context, indexURL string) (*IndexState, error) {
	var (
		st  IndexState
		v   sql.NullInt64
		ss  sql.NullString
		nca sql.NullTime
	)
	err := s.db.QueryRowContext(ctx,
		`SELECT index_version, sync_status, next_crawl_at FROM crawler_index WHERE index_url=$1`, indexURL).
		Scan(&v, &ss, &nca)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("state: GetIndex: %w", err)
	}
	st.IndexVersion, st.SyncStatus, st.NextCrawlAt = v.Int64, ss.String, nca.Time
	return &st, nil
}

// UpsertIndex records an index's last-seen version + sync outcome.
func (s *Store) UpsertIndex(ctx context.Context, indexURL, participantID, source string, version int64, syncStatus string, nextCrawlAt time.Time) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO crawler_index
		   (index_url, participant_id, source, index_version, sync_status, last_crawled_at, next_crawl_at, updated_at)
		 VALUES ($1,$2,$3,$4,$5, now(), $6, now())
		 ON CONFLICT (index_url) DO UPDATE SET
		   participant_id = EXCLUDED.participant_id,
		   source         = EXCLUDED.source,
		   index_version  = EXCLUDED.index_version,
		   sync_status    = EXCLUDED.sync_status,
		   last_crawled_at = now(),
		   next_crawl_at  = EXCLUDED.next_crawl_at,
		   updated_at     = now()`,
		indexURL, nullStr(participantID), nullStr(source), version, nullStr(syncStatus), nullTime(nextCrawlAt))
	if err != nil {
		return fmt.Errorf("state: UpsertIndex: %w", err)
	}
	return nil
}

// GetCatalogVersion returns a catalog's applied-version cursor and whether
// it has ever been synced.
func (s *Store) GetCatalogVersion(ctx context.Context, catalogID string) (version int64, seen bool, err error) {
	var v sql.NullInt64
	err = s.db.QueryRowContext(ctx, `SELECT version FROM crawler_catalog WHERE catalog_id=$1`, catalogID).Scan(&v)
	if err == sql.ErrNoRows {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, fmt.Errorf("state: GetCatalogVersion: %w", err)
	}
	return v.Int64, true, nil
}

// CatalogState is the settled per-catalog outcome to persist.
type CatalogState struct {
	CatalogID     string
	IndexURL      string
	ParticipantID string
	Version       int64
	Status        string // active | retired
	PushStatus    string
	Reason        string
	HTTPStatus    int
}

// execer is satisfied by both *sql.DB and *sql.Tx, so upsertCatalog can run
// standalone or inside Complete's transaction.
type execer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

func upsertCatalog(ctx context.Context, ex execer, c CatalogState) error {
	_, err := ex.ExecContext(ctx,
		`INSERT INTO crawler_catalog
		   (catalog_id, index_url, participant_id, version, status, push_status, reason, http_status, last_pushed_at, updated_at)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8, now(), now())
		 ON CONFLICT (catalog_id) DO UPDATE SET
		   index_url      = EXCLUDED.index_url,
		   participant_id = EXCLUDED.participant_id,
		   version        = EXCLUDED.version,
		   status         = EXCLUDED.status,
		   push_status    = EXCLUDED.push_status,
		   reason         = EXCLUDED.reason,
		   http_status    = EXCLUDED.http_status,
		   last_pushed_at = now(),
		   updated_at     = now()`,
		c.CatalogID, c.IndexURL, nullStr(c.ParticipantID), c.Version, c.Status,
		nullStr(c.PushStatus), nullStr(c.Reason), nullIntZero(c.HTTPStatus))
	if err != nil {
		return fmt.Errorf("state: upsertCatalog: %w", err)
	}
	return nil
}

// UpsertCatalog writes a catalog's settled state (cursor + push outcome).
func (s *Store) UpsertCatalog(ctx context.Context, c CatalogState) error {
	return upsertCatalog(ctx, s.db, c)
}

// RecordFailure records a push failure WITHOUT advancing the version cursor,
// so a failed catalog is never treated as applied (it keeps retrying via the
// queue). On a first-ever failure the row is inserted with a NULL version.
func (s *Store) RecordFailure(ctx context.Context, catalogID, indexURL, participantID, pushStatus, reason string, httpStatus int) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO crawler_catalog
		   (catalog_id, index_url, participant_id, status, push_status, reason, http_status, updated_at)
		 VALUES ($1,$2,$3,'active',$4,$5,$6, now())
		 ON CONFLICT (catalog_id) DO UPDATE SET
		   index_url      = EXCLUDED.index_url,
		   participant_id = EXCLUDED.participant_id,
		   push_status    = EXCLUDED.push_status,
		   reason         = EXCLUDED.reason,
		   http_status    = EXCLUDED.http_status,
		   updated_at     = now()`, // version deliberately not touched
		catalogID, nullStr(indexURL), nullStr(participantID), nullStr(pushStatus), nullStr(reason), nullIntZero(httpStatus))
	if err != nil {
		return fmt.Errorf("state: RecordFailure: %w", err)
	}
	return nil
}

// --- null helpers: map Go zero values to SQL NULL ---

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

func nullTime(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return t
}
