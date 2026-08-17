BEGIN;
ALTER TABLE usage_events ADD COLUMN aggregated_at timestamptz;

-- Migration 24 briefly allowed legacy events to be regrouped by application.
-- These rows have no frozen price and no charge; retain the original legacy
-- account aggregates and remove only the unbilled duplicate projections.
DELETE FROM usage_aggregates a
WHERE a.user_app_id IS NOT NULL
  AND a.price_version_id IS NULL
  AND a.sealed_at IS NULL
  AND NOT EXISTS (
      SELECT 1 FROM usage_charges c
      WHERE c.user_id=a.user_id
        AND c.user_app_id IS NOT DISTINCT FROM a.user_app_id
        AND c.usage_code=a.usage_code
        AND c.window_start=a.window_start
        AND c.window_end=a.window_end
  );

UPDATE usage_events SET aggregated_at=now() WHERE aggregated_at IS NULL;
CREATE INDEX usage_events_unaggregated_idx ON usage_events(created_at) WHERE aggregated_at IS NULL;
COMMIT;
