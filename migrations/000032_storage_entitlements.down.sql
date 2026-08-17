BEGIN;
ALTER TABLE user_subscriptions DROP CONSTRAINT IF EXISTS user_subscriptions_storage_snapshot_check;
ALTER TABLE plan_versions DROP CONSTRAINT IF EXISTS plan_versions_storage_entitlements_check;
ALTER TABLE app_product_versions DROP CONSTRAINT IF EXISTS app_product_versions_runtime_storage_check;
DROP FUNCTION IF EXISTS runtime_storage_spec_valid(jsonb);
COMMIT;
