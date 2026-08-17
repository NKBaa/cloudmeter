BEGIN;
DROP FUNCTION IF EXISTS grant_subscription_credit(uuid,bigint,uuid);
ALTER TABLE user_subscriptions DROP CONSTRAINT IF EXISTS user_subscriptions_credit_grant_check;
ALTER TABLE user_subscriptions ADD CONSTRAINT user_subscriptions_credit_grant_check CHECK (
    jsonb_typeof(entitlements_snapshot->'creditGrantCents') = 'number'
    AND (entitlements_snapshot->>'creditGrantCents')::bigint >= 0
);
ALTER TABLE plan_versions DROP CONSTRAINT IF EXISTS plan_versions_credit_grant_check;
ALTER TABLE plan_versions ADD CONSTRAINT plan_versions_credit_grant_check CHECK (
    jsonb_typeof(entitlements->'creditGrantCents') = 'number'
    AND (entitlements->>'creditGrantCents')::bigint >= 0
);
COMMIT;
