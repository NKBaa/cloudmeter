BEGIN;
DROP INDEX IF EXISTS usage_events_unaggregated_idx;
ALTER TABLE usage_events DROP COLUMN IF EXISTS aggregated_at;
COMMIT;
