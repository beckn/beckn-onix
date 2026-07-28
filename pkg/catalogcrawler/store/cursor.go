package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"
)

// GetCatalogVersion returns a catalog's applied-version cursor and whether it
// has ever been synced.
func (s *Store) GetCatalogVersion(ctx context.Context, catalogID string) (version int64, seen bool, err error) {
	var v sql.NullInt64
	err = s.db.QueryRowContext(ctx, `SELECT version FROM crawler_catalog WHERE catalog_id=$1`, catalogID).Scan(&v)
	if err == sql.ErrNoRows {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, fmt.Errorf("store: GetCatalogVersion: %w", err)
	}
	return v.Int64, true, nil
}

// PassReport is one settled pass's detailed outcome, appended to a catalog's
// push_status history array. Counts are what this pass actually pushed; on a
// partial/faulted push, BatchesAcked < BatchesTotal tells the story. Outcome/Mode
// carry the Catalog Sync's wire values (see pkg catalog's SyncOutcome).
type PassReport struct {
	At           time.Time `json:"ts"`
	FromVersion  int64     `json:"from"`
	ToVersion    int64     `json:"to"`
	Mode         string    `json:"mode,omitempty"` // FULL | MERGE ("" for retire/skip)
	Resources    int       `json:"resources"`      // resources pushed this pass
	Offers       int       `json:"offers"`         // offers pushed this pass
	Removals     int       `json:"removals"`       // resources+offers removed this pass
	BatchesAcked int       `json:"batchesAcked"`
	BatchesTotal int       `json:"batchesTotal"`
	Outcome      string    `json:"outcome"` // pushed | partial | skipped | dropped | retired | faulted
	HTTPStatus   int       `json:"httpStatus,omitempty"`
	Reason       string    `json:"reason,omitempty"`
}

// CatalogState is the settled per-catalog outcome to persist. Report is
// appended to the push_status history array; Reason/HTTPStatus mirror the
// latest pass for cheap top-level queries.
type CatalogState struct {
	CatalogID     string
	IndexURL      string
	ParticipantID string
	Version       int64
	Status        string // active | retired (catalog.CatalogStatus wire value)
	Report        PassReport
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

func upsertCatalog(ctx context.Context, ex execer, c CatalogState) error {
	rep, err := json.Marshal(c.Report)
	if err != nil {
		return fmt.Errorf("store: upsertCatalog marshal report: %w", err)
	}
	_, err = ex.ExecContext(ctx,
		`INSERT INTO crawler_catalog
		   (catalog_id, index_url, participant_id, version, status, push_status, reason, http_status, last_pushed_at, updated_at)
		 VALUES ($1,$2,$3,$4,$5, jsonb_build_array($6::jsonb), $7, $8, now(), now())
		 ON CONFLICT (catalog_id) DO UPDATE SET
		   index_url      = EXCLUDED.index_url,
		   participant_id = EXCLUDED.participant_id,
		   version        = EXCLUDED.version,
		   status         = EXCLUDED.status,
		   push_status    = `+appendPassClause+`,
		   reason         = EXCLUDED.reason,
		   http_status    = EXCLUDED.http_status,
		   last_pushed_at = now(),
		   updated_at     = now()`,
		c.CatalogID, c.IndexURL, nullStr(c.ParticipantID), c.Version, c.Status,
		string(rep), nullStr(c.Report.Reason), nullIntZero(c.Report.HTTPStatus))
	if err != nil {
		return fmt.Errorf("store: upsertCatalog: %w", err)
	}
	return nil
}

// UpsertCatalog writes a catalog's settled state (cursor + push outcome).
func (s *Store) UpsertCatalog(ctx context.Context, c CatalogState) error {
	return upsertCatalog(ctx, s.db, c)
}

// GetCatalogReports returns a catalog's pass history (oldest -> newest), decoded
// from the push_status jsonb array. Empty if the catalog has never settled.
func (s *Store) GetCatalogReports(ctx context.Context, catalogID string) ([]PassReport, error) {
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
	var reports []PassReport
	if err := json.Unmarshal(raw, &reports); err != nil {
		return nil, fmt.Errorf("store: GetCatalogReports decode: %w", err)
	}
	return reports, nil
}

// RecordFailure records a push failure WITHOUT advancing the version cursor, so
// a failed catalog is never treated as applied (it keeps retrying via the
// queue). On a first-ever failure the row is inserted with a NULL version.
func (s *Store) RecordFailure(ctx context.Context, catalogID, indexURL, participantID string, report PassReport) error {
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
