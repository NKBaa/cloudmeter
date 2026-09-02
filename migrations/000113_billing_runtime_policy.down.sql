BEGIN;

DELETE FROM user_notifications WHERE kind = 'billing_app_deleted';
ALTER TABLE user_notifications DROP CONSTRAINT user_notifications_kind_check;
ALTER TABLE user_notifications ADD CONSTRAINT user_notifications_kind_check CHECK (kind IN (
    'low_balance','billing_suspended','billing_recovered',
    'subscription_purchased','subscription_purchase_failed',
    'subscription_expiring','subscription_grace','subscription_expired'
));

DROP INDEX IF EXISTS user_apps_billing_suspended_cleanup_idx;
ALTER TABLE user_apps DROP COLUMN IF EXISTS billing_suspended_at;
ALTER TABLE system_settings
    DROP COLUMN IF EXISTS billing_suspended_delete_days,
    DROP COLUMN IF EXISTS startup_balance_reserve_cents;

COMMIT;
