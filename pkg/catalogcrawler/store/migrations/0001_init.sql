-- Phase-1 crawler state: three tables, version-based. Idempotent
-- (IF NOT EXISTS) so Migrate can run safely on every startup.

CREATE TABLE IF NOT EXISTS crawler_index (
  index_url       text PRIMARY KEY,
  participant_id  text,
  source          text CHECK (source IN ('registry','config','on_demand')),
  index_version   bigint,
  sync_status     text CHECK (sync_status IN ('pending','ok','partial','failed')),
  last_crawled_at timestamptz,
  next_crawl_at   timestamptz,
  updated_at      timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS crawler_queue (
  id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  catalog_id      text NOT NULL,
  index_url       text NOT NULL,
  from_version    bigint,
  to_version      bigint NOT NULL,
  op              text NOT NULL DEFAULT 'sync' CHECK (op IN ('sync','retire')),
  status          text NOT NULL DEFAULT 'queued' CHECK (status IN ('queued','in_progress','failed')),
  attempts        integer NOT NULL DEFAULT 0,
  next_attempt_at timestamptz NOT NULL DEFAULT now(),
  claimed_at      timestamptz,
  enqueued_at     timestamptz NOT NULL DEFAULT now(),
  UNIQUE (catalog_id)
);
CREATE INDEX IF NOT EXISTS ix_crawler_queue_ready
  ON crawler_queue (next_attempt_at) WHERE claimed_at IS NULL;

CREATE TABLE IF NOT EXISTS crawler_catalog (
  catalog_id     text PRIMARY KEY,
  index_url      text NOT NULL,
  participant_id text,
  version        bigint,
  status         text NOT NULL DEFAULT 'active' CHECK (status IN ('active','retired')),
  push_status    text CHECK (push_status IN ('pending','pushed','rejected','failed','skipped')),
  reason         text,
  http_status    integer,
  last_pushed_at timestamptz,
  updated_at     timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS ix_crawler_catalog_index ON crawler_catalog (index_url);
