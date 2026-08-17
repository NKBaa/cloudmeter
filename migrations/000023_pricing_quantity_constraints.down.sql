BEGIN;
ALTER TABLE pricing_versions
    DROP CONSTRAINT IF EXISTS pricing_versions_minimum_quantity_nonnegative,
    DROP CONSTRAINT IF EXISTS pricing_versions_free_quantity_nonnegative;
COMMIT;
