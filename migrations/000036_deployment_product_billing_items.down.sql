BEGIN;
DELETE FROM pricing_items WHERE code IN ('app.deployment','product.authorization');
COMMIT;
