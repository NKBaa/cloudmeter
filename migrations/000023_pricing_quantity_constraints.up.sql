BEGIN;
ALTER TABLE pricing_versions
    ADD CONSTRAINT pricing_versions_minimum_quantity_nonnegative CHECK (minimum_quantity >= 0),
    ADD CONSTRAINT pricing_versions_free_quantity_nonnegative CHECK (free_quantity >= 0);
COMMIT;
