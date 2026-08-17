BEGIN;
DROP TRIGGER IF EXISTS app_products_identity_immutable ON app_products;
DROP FUNCTION IF EXISTS protect_app_product_identity;
COMMIT;
