BEGIN;
ALTER TABLE system_state DROP COLUMN IF EXISTS log_retention_bytes;
ALTER TABLE system_state DROP COLUMN IF EXISTS log_retention_hours;
DROP INDEX IF EXISTS app_product_versions_available_idx_2;
ALTER TABLE app_product_versions DROP COLUMN IF EXISTS deleted_at;
COMMIT;
