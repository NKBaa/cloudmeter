BEGIN;
ALTER TABLE plan_versions ADD CONSTRAINT plan_versions_allowed_products_check CHECK (
    NOT (entitlements ? 'allowedProductIds')
    OR jsonb_typeof(entitlements->'allowedProductIds') = 'array'
);
ALTER TABLE user_subscriptions ADD CONSTRAINT user_subscriptions_allowed_products_snapshot_check CHECK (
    NOT (entitlements_snapshot ? 'allowedProductIds')
    OR jsonb_typeof(entitlements_snapshot->'allowedProductIds') = 'array'
);
COMMIT;
