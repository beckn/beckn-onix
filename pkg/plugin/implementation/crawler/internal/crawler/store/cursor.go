package store

// cursor.go — the per-catalog version cursor and pass-report history:
// GetCatalogVersion, the append-and-trim push_status upsert (upsertCatalog,
// shared with Complete's transaction), RecordFailure (which deliberately never
// advances the cursor), and report readback.

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"github.com/beckn-one/beckn-onix/pkg/plugin/implementation/crawler/internal/crawler/catalog"
)

// GetCatalogVersion returns a catalog's applied content-lineage version and
// entry-level cursors, and whether it has ever been synced. The two cursors
// are independent (RFC NFH-014 §Versioning) -- see catalog/change.go.
func (s *Store) GetCatalogVersion(ctx context.Context, catalogID string) (version, entryVersion int64, seen bool, err error) {
	var v, ev sql.NullInt64
	err = s.db.QueryRowContext(ctx, `SELECT version, entry_version FROM crawler_catalog WHERE catalog_id=$1`, catalogID).Scan(&v, &ev)
	if err == sql.ErrNoRows {
		return 0, 0, false, nil
	}
	if err != nil {
		return 0, 0, false, fmt.Errorf("store: GetCatalogVersion: %w", err)
	}
	return v.Int64, ev.Int64, true, nil
}

// appendPassClause is the ON CONFLICT expression that appends the incoming
// single-element push_status array to the existing history and trims to the
// most recent passHistoryCap entries (chronological order preserved).
var appendPassClause = fmt.Sprintf(`(
		   SELECT COALESCE(jsonb_agg(e ORDER BY n), '[]'::jsonb)
		     FROM jsonb_array_elements(COALESCE(crawler_catalog.push_status,'[]'::jsonb) || EXCLUDED.push_status)
		          WITH ORDINALITY AS t(e, n)
		    WHERE n > jsonb_array_length(COALESCE(crawler_catalog.push_status,'[]'::jsonb) || EXCLUDED.push_status) - %d
		 )`, passHistoryCap)

func upsertCatalog(ctx context.Context, ex execer, c catalog.CatalogState) error {
	rep, err := json.Marshal(c.Report)
	if err != nil {
		return fmt.Errorf("store: upsertCatalog marshal report: %w", err)
	}
	// descriptor/provider/catalog_type use COALESCE(EXCLUDED, existing) rather
	// than a plain overwrite: a skip or a recorded failure settles through this
	// same path without ever populating them, and must not blank out the
	// envelope a previous successful sync wrote -- that envelope is what a
	// later retire needs once the index stops carrying any content to refetch.
	_, err = ex.ExecContext(ctx,
		`INSERT INTO crawler_catalog
		   (catalog_id, index_url, participant_id, version, entry_version, status, push_status, reason, http_status, descriptor, provider, catalog_type, last_pushed_at, updated_at)
		 VALUES ($1,$2,$3,$4,$5,$6, jsonb_build_array($7::jsonb), $8, $9, $10, $11, $12, now(), now())
		 ON CONFLICT (catalog_id) DO UPDATE SET
		   index_url      = EXCLUDED.index_url,
		   participant_id = EXCLUDED.participant_id,
		   version        = EXCLUDED.version,
		   entry_version  = EXCLUDED.entry_version,
		   status         = EXCLUDED.status,
		   push_status    = `+appendPassClause+`,
		   reason         = EXCLUDED.reason,
		   http_status    = EXCLUDED.http_status,
		   descriptor     = COALESCE(EXCLUDED.descriptor, crawler_catalog.descriptor),
		   provider       = COALESCE(EXCLUDED.provider, crawler_catalog.provider),
		   catalog_type   = COALESCE(EXCLUDED.catalog_type, crawler_catalog.catalog_type),
		   last_pushed_at = now(),
		   updated_at     = now()`,
		c.CatalogID, c.IndexURL, nullStr(c.ParticipantID), c.Version, c.EntryVersion, c.Status,
		string(rep), nullStr(c.Report.Reason), nullIntZero(c.Report.HTTPStatus),
		nullBytes(c.Descriptor), nullBytes(c.Provider), nullStr(c.CatalogType))
	if err != nil {
		return fmt.Errorf("store: upsertCatalog: %w", err)
	}
	return nil
}

// GetCatalogEnvelope returns the minimal envelope (id is the caller's own
// catalogID; descriptor/provider/catalogType/participantID) captured from this
// catalog's last successful ACTIVE settle -- what a retire needs to build a
// Discovery wipe push once the index entry no longer carries any content to
// fetch. ok is false if the catalog was never settled, or settled without ever
// carrying an envelope (e.g. every pass so far was a content-invalid skip).
func (s *Store) GetCatalogEnvelope(ctx context.Context, catalogID string) (descriptor, provider []byte, catalogType, participantID string, ok bool, err error) {
	var d, p sql.NullString
	var ct, pid sql.NullString
	err = s.db.QueryRowContext(ctx,
		`SELECT descriptor, provider, catalog_type, participant_id FROM crawler_catalog WHERE catalog_id=$1`,
		catalogID).Scan(&d, &p, &ct, &pid)
	if err == sql.ErrNoRows {
		return nil, nil, "", "", false, nil
	}
	if err != nil {
		return nil, nil, "", "", false, fmt.Errorf("store: GetCatalogEnvelope: %w", err)
	}
	if !d.Valid || !p.Valid {
		return nil, nil, "", "", false, nil
	}
	return []byte(d.String), []byte(p.String), ct.String, pid.String, true, nil
}

// UpsertCatalog writes a catalog's settled state (cursor + push outcome).
func (s *Store) UpsertCatalog(ctx context.Context, c catalog.CatalogState) error {
	return upsertCatalog(ctx, s.db, c)
}

// CountParked returns the number of permanently-failed queue items (status
// 'failed') — the gauge of inventory currently missing from Discovery.
func (s *Store) CountParked(ctx context.Context) (int, error) {
	var n int
	if err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM crawler_queue WHERE status='failed'`).Scan(&n); err != nil {
		return 0, fmt.Errorf("store: CountParked: %w", err)
	}
	return n, nil
}

// CountTracked returns the number of catalogs the crawler tracks — the coverage
// gauge (how much inventory we serve).
func (s *Store) CountTracked(ctx context.Context) (int, error) {
	var n int
	if err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM crawler_catalog`).Scan(&n); err != nil {
		return 0, fmt.Errorf("store: CountTracked: %w", err)
	}
	return n, nil
}

// GetCatalogReports returns a catalog's pass history (oldest -> newest), decoded
// from the push_status jsonb array. Empty if the catalog has never settled.
func (s *Store) GetCatalogReports(ctx context.Context, catalogID string) ([]catalog.PassReport, error) {
	var raw []byte
	err := s.db.QueryRowContext(ctx, `SELECT push_status FROM crawler_catalog WHERE catalog_id=$1`, catalogID).Scan(&raw)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("store: GetCatalogReports: %w", err)
	}
	if len(raw) == 0 {
		return nil, nil
	}
	var reports []catalog.PassReport
	if err := json.Unmarshal(raw, &reports); err != nil {
		return nil, fmt.Errorf("store: GetCatalogReports decode: %w", err)
	}
	return reports, nil
}

// RecordFailure records a push failure WITHOUT advancing the version cursor, so
// a failed catalog is never treated as applied (it keeps retrying via the
// queue). On a first-ever failure the row is inserted with a NULL version.
func (s *Store) RecordFailure(ctx context.Context, catalogID, indexURL, participantID string, report catalog.PassReport) error {
	rep, err := json.Marshal(report)
	if err != nil {
		return fmt.Errorf("store: RecordFailure marshal report: %w", err)
	}
	_, err = s.db.ExecContext(ctx,
		`INSERT INTO crawler_catalog
		   (catalog_id, index_url, participant_id, status, push_status, reason, http_status, updated_at)
		 VALUES ($1,$2,$3,'active', jsonb_build_array($4::jsonb), $5, $6, now())
		 ON CONFLICT (catalog_id) DO UPDATE SET
		   index_url      = EXCLUDED.index_url,
		   participant_id = EXCLUDED.participant_id,
		   push_status    = `+appendPassClause+`,
		   reason         = EXCLUDED.reason,
		   http_status    = EXCLUDED.http_status,
		   updated_at     = now()`, // version deliberately not touched
		catalogID, nullStr(indexURL), nullStr(participantID), string(rep), nullStr(report.Reason), nullIntZero(report.HTTPStatus))
	if err != nil {
		return fmt.Errorf("store: RecordFailure: %w", err)
	}
	return nil
}
