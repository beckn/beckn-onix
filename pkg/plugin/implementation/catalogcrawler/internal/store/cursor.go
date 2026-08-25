package store

// cursor.go — the per-catalog cursor (crawlmanager.CatalogCursor), its
// captured descriptor/provider/catalogType envelope (for a later retire's
// wipe push), and the latest-failure reason. Simpler than the prototype this
// schema came from: crawlmanager.PassReport has no push-outcome/HTTP-status
// detail to keep a jsonb history of, so a failure just overwrites
// crawler_catalog.reason rather than appending to an array.

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/beckn/catalog-core/pkg/catalog/crawlmanager"
)

// GetCatalogCursor returns a catalog's stored cursor, and whether it has ever
// been synced.
func (s *Store) GetCatalogCursor(ctx context.Context, catalogID string) (crawlmanager.CatalogCursor, bool, error) {
	var (
		c        crawlmanager.CatalogCursor
		indexURL sql.NullString
		pid      sql.NullString
		v, ev    sql.NullInt64
		status   sql.NullString
	)
	err := s.db.QueryRowContext(ctx,
		`SELECT index_url, participant_id, version, entry_version, status FROM crawler_catalog WHERE catalog_id=$1`, catalogID).
		Scan(&indexURL, &pid, &v, &ev, &status)
	if err == sql.ErrNoRows {
		return crawlmanager.CatalogCursor{}, false, nil
	}
	if err != nil {
		return crawlmanager.CatalogCursor{}, false, fmt.Errorf("store: GetCatalogCursor: %w", err)
	}
	c.CatalogID, c.IndexURL, c.ParticipantID = catalogID, indexURL.String, pid.String
	c.Version, c.EntryVersion = v.Int64, ev.Int64
	c.Retired = status.String == "retired"
	return c, true, nil
}

// GetCatalogEnvelope returns the descriptor/provider/catalogType/
// participantId captured from catalogID's last successful sync, or
// ok=false if it was never captured.
func (s *Store) GetCatalogEnvelope(ctx context.Context, catalogID string) (descriptor, provider json.RawMessage, catalogType, participantID string, ok bool, err error) {
	var d, p, ct, pid sql.NullString
	err = s.db.QueryRowContext(ctx,
		`SELECT descriptor, provider, catalog_type, participant_id FROM crawler_catalog WHERE catalog_id=$1`, catalogID).
		Scan(&d, &p, &ct, &pid)
	if err == sql.ErrNoRows {
		return nil, nil, "", "", false, nil
	}
	if err != nil {
		return nil, nil, "", "", false, fmt.Errorf("store: GetCatalogEnvelope: %w", err)
	}
	if !d.Valid && !p.Valid {
		return nil, nil, "", "", false, nil
	}
	return json.RawMessage(d.String), json.RawMessage(p.String), ct.String, pid.String, true, nil
}

// upsertCatalog writes cursor's settled state, including its captured
// envelope (COALESCE(EXCLUDED, existing): a retire settle or a recorded
// failure passes no envelope of its own and must not blank out one a
// previous successful sync wrote -- that envelope is what a later retire
// needs once the index stops carrying any content to refetch). Clears
// reason unconditionally: this only ever runs on a successful settle (via
// Complete), so any reason RecordFailure previously stamped no longer
// describes this catalog's current state and would otherwise report a
// resolved failure as still-current to a status query. Runs standalone or
// inside Complete's transaction via execer.
func upsertCatalog(ctx context.Context, ex execer, c crawlmanager.CatalogCursor) error {
	status := "active"
	if c.Retired {
		status = "retired"
	}
	_, err := ex.ExecContext(ctx,
		`INSERT INTO crawler_catalog (catalog_id, index_url, participant_id, version, entry_version, status, descriptor, provider, catalog_type, updated_at)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9, now())
		 ON CONFLICT (catalog_id) DO UPDATE SET
		   index_url      = EXCLUDED.index_url,
		   participant_id = EXCLUDED.participant_id,
		   version        = EXCLUDED.version,
		   entry_version  = EXCLUDED.entry_version,
		   status         = EXCLUDED.status,
		   reason         = NULL,
		   descriptor     = COALESCE(EXCLUDED.descriptor, crawler_catalog.descriptor),
		   provider       = COALESCE(EXCLUDED.provider, crawler_catalog.provider),
		   catalog_type   = COALESCE(EXCLUDED.catalog_type, crawler_catalog.catalog_type),
		   updated_at     = now()`,
		c.CatalogID, nullStr(c.IndexURL), nullStr(c.ParticipantID), c.Version, c.EntryVersion, status,
		nullBytes(c.Descriptor), nullBytes(c.Provider), nullStr(c.CatalogType))
	if err != nil {
		return fmt.Errorf("store: upsertCatalog: %w", err)
	}
	return nil
}

// RecordFailure records the latest failure reason WITHOUT advancing the
// version cursor, so a failed catalog is never treated as applied (it keeps
// retrying via the queue). On a first-ever failure the row is inserted with a
// NULL version.
func (s *Store) RecordFailure(ctx context.Context, report crawlmanager.PassReport) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO crawler_catalog (catalog_id, index_url, participant_id, status, reason, updated_at)
		 VALUES ($1,$2,$3,'active',$4, now())
		 ON CONFLICT (catalog_id) DO UPDATE SET
		   index_url      = EXCLUDED.index_url,
		   participant_id = EXCLUDED.participant_id,
		   reason         = EXCLUDED.reason,
		   updated_at     = now()`, // version/status deliberately not touched
		report.CatalogID, nullStr(report.IndexURL), nullStr(report.ParticipantID), nullStr(report.Error))
	if err != nil {
		return fmt.Errorf("store: RecordFailure: %w", err)
	}
	return nil
}
