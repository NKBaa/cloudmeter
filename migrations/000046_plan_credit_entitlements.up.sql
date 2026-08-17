BEGIN;

-- Every plan version carries an explicit, integer monthly credit allowance.
-- Existing versions are backfilled with zero so older snapshots remain valid.
UPDATE plan_versions
SET entitlements = entitlements || jsonb_build_object(
    'creditGrantCents', coalesce((entitlements->>'creditGrantCents')::bigint, 0)
);
UPDATE user_subscriptions
SET entitlements_snapshot = entitlements_snapshot || jsonb_build_object(
    'creditGrantCents', coalesce((entitlements_snapshot->>'creditGrantCents')::bigint, 0)
);

ALTER TABLE plan_versions
    ADD CONSTRAINT plan_versions_credit_grant_check CHECK (
        jsonb_typeof(entitlements->'creditGrantCents') = 'number'
        AND (entitlements->>'creditGrantCents')::bigint >= 0
    );
ALTER TABLE user_subscriptions
    ADD CONSTRAINT user_subscriptions_credit_grant_check CHECK (
        jsonb_typeof(entitlements_snapshot->'creditGrantCents') = 'number'
        AND (entitlements_snapshot->>'creditGrantCents')::bigint >= 0
    );

CREATE INDEX credit_grants_subscription_month_idx
    ON credit_grants(user_id, business_ref)
    WHERE business_ref LIKE 'subscription-credit/%';

COMMIT;
