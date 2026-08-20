-- The entry-level cursor: entryVersion bumps on ANY change to a
-- catalog-index entry (content or metadata), independent of the
-- content-lineage `version` column already here. crawler_queue carries it
-- through an enqueued item so the catalog job can persist it at settle time
-- without re-reading the index entry just for this number.
ALTER TABLE crawler_catalog ADD COLUMN IF NOT EXISTS entry_version bigint;
ALTER TABLE crawler_queue   ADD COLUMN IF NOT EXISTS entry_version bigint;
