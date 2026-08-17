BEGIN;
ALTER TABLE user_subscriptions DROP CONSTRAINT IF EXISTS user_subscriptions_cycle_price_snapshot_check;
ALTER TABLE user_subscriptions DROP COLUMN IF EXISTS entitlements_snapshot;
ALTER TABLE user_subscriptions DROP COLUMN IF EXISTS cycle_price_cents_snapshot;
COMMIT;
