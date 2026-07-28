-- Conditional-GET validators for the index: opportunistic ETag /
-- Last-Modified so an unchanged index can answer 304 Not Modified and skip
-- the re-download. Both nullable — a host that sends neither simply falls
-- back to the version-based change gate, so correctness never depends on them.
ALTER TABLE crawler_index ADD COLUMN IF NOT EXISTS etag          text;
ALTER TABLE crawler_index ADD COLUMN IF NOT EXISTS last_modified text;
