BEGIN;
DROP INDEX IF EXISTS usage_events_public_ingress_app_uidx;
DELETE FROM pricing_items WHERE code='network.public_ingress';
ALTER TABLE user_subscriptions DROP CONSTRAINT IF EXISTS user_subscriptions_public_ingress_check;
ALTER TABLE plan_versions DROP CONSTRAINT IF EXISTS plan_versions_public_ingress_check;
COMMIT;
