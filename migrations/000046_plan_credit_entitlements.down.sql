BEGIN;
DROP INDEX IF EXISTS credit_grants_subscription_month_idx;
ALTER TABLE user_subscriptions DROP CONSTRAINT IF EXISTS user_subscriptions_credit_grant_check;
ALTER TABLE plan_versions DROP CONSTRAINT IF EXISTS plan_versions_credit_grant_check;
UPDATE user_subscriptions SET entitlements_snapshot = entitlements_snapshot - 'creditGrantCents';
UPDATE plan_versions SET entitlements = entitlements - 'creditGrantCents';
COMMIT;
