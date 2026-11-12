-- Forget every index's conditional-GET validators at startup, so the first
-- poll after a start reads each index in full instead of taking a 304.
-- A stored validator means "this build already decided every entry of that
-- index"; a different build may decide the same entries differently (a
-- tombstone an earlier build skipped as a rollback, for one), and with a
-- 304 it would not look at them until the publisher's next publish.
-- Migrate runs on every startup; the cost is one full index read per index
-- per start.
UPDATE crawler_index SET etag = NULL, last_modified = NULL
 WHERE etag IS NOT NULL OR last_modified IS NOT NULL;
