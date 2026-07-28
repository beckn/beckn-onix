-- Detailed per-pass report. push_status stops being a single value and becomes
-- a jsonb ARRAY of pass records (appended per settle, capped in code to the most
-- recent entries). Each element:
--   {ts, from, to, mode, resources, offers, removals,
--    batchesAcked, batchesTotal, outcome, httpStatus, reason}
-- The latest element's "outcome" is the current push outcome. status
-- (active/retired) is unchanged. Guarded so re-running Migrate is a no-op
-- (Migrate replays every file on each startup).
DO $$
BEGIN
  IF (SELECT data_type FROM information_schema.columns
        WHERE table_schema = current_schema()
          AND table_name = 'crawler_catalog' AND column_name = 'push_status') = 'text' THEN
    ALTER TABLE crawler_catalog DROP CONSTRAINT IF EXISTS crawler_catalog_push_status_check;
    ALTER TABLE crawler_catalog
      ALTER COLUMN push_status DROP DEFAULT,
      ALTER COLUMN push_status TYPE jsonb USING (
        CASE
          WHEN push_status IS NULL OR push_status = '' THEN '[]'::jsonb
          ELSE jsonb_build_array(jsonb_build_object('outcome', push_status))
        END
      );
    ALTER TABLE crawler_catalog ALTER COLUMN push_status SET DEFAULT '[]'::jsonb;
  END IF;
END $$;
