BEGIN;

ALTER TABLE app_product_versions
    DROP CONSTRAINT IF EXISTS app_product_versions_update_spec_check,
    DROP COLUMN IF EXISTS update_spec;

COMMIT;
