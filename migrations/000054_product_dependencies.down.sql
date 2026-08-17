BEGIN;
DROP TRIGGER IF EXISTS app_product_versions_dependency_insert_guard ON app_product_versions;
DROP TRIGGER IF EXISTS app_product_versions_dependency_guard ON app_product_versions;
DROP FUNCTION IF EXISTS protect_published_product_dependencies();
ALTER TABLE app_product_versions DROP CONSTRAINT IF EXISTS app_product_versions_runtime_dependencies_check;
DROP FUNCTION IF EXISTS runtime_dependencies_spec_valid(jsonb);
COMMIT;
