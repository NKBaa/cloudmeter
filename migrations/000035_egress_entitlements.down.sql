BEGIN;
ALTER TABLE user_apps DROP CONSTRAINT IF EXISTS user_apps_suspension_reason_check;
ALTER TABLE user_apps ADD CONSTRAINT user_apps_suspension_reason_check CHECK
 (suspension_reason IS NULL OR suspension_reason IN ('billing_insufficient','subscription_expired'));
ALTER TABLE user_subscriptions DROP CONSTRAINT IF EXISTS user_subscriptions_egress_snapshot_check;
ALTER TABLE plan_versions DROP CONSTRAINT IF EXISTS plan_versions_egress_entitlements_check;
COMMIT;
