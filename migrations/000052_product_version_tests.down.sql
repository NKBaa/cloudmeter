BEGIN;
DROP TRIGGER IF EXISTS app_product_versions_configuration_immutable ON app_product_versions;
DROP FUNCTION IF EXISTS protect_product_version_configuration();
DROP TRIGGER IF EXISTS app_product_version_tests_snapshot_immutable ON app_product_version_tests;
DROP FUNCTION IF EXISTS protect_product_version_test_snapshot();
DROP TABLE IF EXISTS app_product_version_tests;
COMMIT;
