BEGIN;

-- Catalog version "delete" removes the template from every listing while the
-- immutable release snapshots and running instances stay untouched. A deleted
-- version is permanently hidden, unlike the reversible archive state.
ALTER TABLE app_product_versions ADD COLUMN IF NOT EXISTS deleted_at timestamptz;
CREATE INDEX IF NOT EXISTS app_product_versions_available_idx_2
    ON app_product_versions(product_id, version DESC)
    WHERE archived_at IS NULL AND deleted_at IS NULL;

-- Global runtime-log retention policy. The worker prunes stored instance logs
-- older than retention_hours or beyond retention_bytes per application.
ALTER TABLE system_state ADD COLUMN IF NOT EXISTS log_retention_hours integer NOT NULL DEFAULT 168 CHECK (log_retention_hours BETWEEN 1 AND 8760);
ALTER TABLE system_state ADD COLUMN IF NOT EXISTS log_retention_bytes bigint NOT NULL DEFAULT 1048576 CHECK (log_retention_bytes BETWEEN 16384 AND 1073741824);

COMMIT;
