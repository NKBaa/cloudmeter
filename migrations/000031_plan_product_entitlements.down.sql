BEGIN;
ALTER TABLE user_subscriptions DROP CONSTRAINT IF EXISTS user_subscriptions_allowed_products_snapshot_check;
ALTER TABLE plan_versions DROP CONSTRAINT IF EXISTS plan_versions_allowed_products_check;
COMMIT;
