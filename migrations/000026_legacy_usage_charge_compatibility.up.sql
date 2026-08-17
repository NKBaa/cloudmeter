BEGIN;
-- Migration 24 projected legacy events into app-scoped rows. They were never
-- charged and have no frozen price; remove only those projections. Account-
-- scoped historical aggregates and charges remain intact.
DELETE FROM usage_aggregates a
WHERE a.user_app_id IS NOT NULL
  AND a.price_version_id IS NULL
  AND a.sealed_at IS NULL
  AND NOT EXISTS (SELECT 1 FROM usage_charges c WHERE c.user_id=a.user_id AND c.user_app_id IS NOT DISTINCT FROM a.user_app_id AND c.usage_code=a.usage_code AND c.window_start=a.window_start AND c.window_end=a.window_end);
COMMIT;
