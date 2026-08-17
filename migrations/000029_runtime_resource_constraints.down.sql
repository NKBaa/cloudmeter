BEGIN;
ALTER TABLE app_product_versions DROP CONSTRAINT IF EXISTS app_product_versions_runtime_resources_check;
COMMIT;
