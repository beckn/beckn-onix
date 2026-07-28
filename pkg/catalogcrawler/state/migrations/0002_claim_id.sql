-- Claim token: each ClaimNext stamps a fresh claim_id so only the holder can
-- settle (Complete/FailQueueItem), and a coalescing Enqueue can't silently
-- reset an in-progress row's claim.
ALTER TABLE crawler_queue ADD COLUMN IF NOT EXISTS claim_id uuid;
