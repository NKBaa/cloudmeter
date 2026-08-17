BEGIN;
ALTER TABLE user_subscriptions DROP CONSTRAINT IF EXISTS user_subscriptions_backup_snapshot_check;
ALTER TABLE plan_versions DROP CONSTRAINT IF EXISTS plan_versions_backup_entitlements_check;
ALTER TABLE app_backups DROP COLUMN IF EXISTS reserved_bytes;
COMMIT;
