-- The minimal envelope (id/descriptor/provider/catalogType) a later retire
-- needs to build a Discovery wipe push, captured from every successful ACTIVE
-- settle so it survives once a retired index entry stops carrying any file
-- reference to fetch content from (RFC NFH-014: baseline/changes/latest are
-- all dropped once retiredAt is set). catalog_id is already the primary key.
ALTER TABLE crawler_catalog ADD COLUMN IF NOT EXISTS descriptor   jsonb;
ALTER TABLE crawler_catalog ADD COLUMN IF NOT EXISTS provider     jsonb;
ALTER TABLE crawler_catalog ADD COLUMN IF NOT EXISTS catalog_type text;
