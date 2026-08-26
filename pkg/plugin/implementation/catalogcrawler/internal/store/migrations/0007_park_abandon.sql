-- Revive-or-abandon parked catalogs (catalog-core's
-- Store.RequeueOrAbandonParked / Store.ListAbandoned): a cumulative
-- park_count per queue row (distinct from attempts, which Reschedule
-- already owns), parked_at to know how long a row has been sitting
-- parked (crawler_queue carries no other updated-at timestamp), and
-- abandoned_at for ListAbandoned's own reporting. 'failed' is kept in the
-- allowed status set for backward compatibility with rows Park wrote
-- before this migration -- new Parks now write 'parked' instead.
ALTER TABLE crawler_queue ADD COLUMN IF NOT EXISTS park_count integer NOT NULL DEFAULT 0;
ALTER TABLE crawler_queue ADD COLUMN IF NOT EXISTS parked_at timestamptz;
ALTER TABLE crawler_queue ADD COLUMN IF NOT EXISTS abandoned_at timestamptz;

ALTER TABLE crawler_queue DROP CONSTRAINT IF EXISTS crawler_queue_status_check;
ALTER TABLE crawler_queue ADD CONSTRAINT crawler_queue_status_check
  CHECK (status IN ('queued','in_progress','failed','parked','abandoned'));
