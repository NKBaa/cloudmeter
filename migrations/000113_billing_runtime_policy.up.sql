BEGIN;

ALTER TABLE system_settings
    ADD COLUMN startup_balance_reserve_cents bigint NOT NULL DEFAULT 0
        CHECK (startup_balance_reserve_cents BETWEEN 0 AND 1000000000000),
    ADD COLUMN billing_suspended_delete_days integer NOT NULL DEFAULT 30
        CHECK (billing_suspended_delete_days BETWEEN 0 AND 3650);

ALTER TABLE user_apps ADD COLUMN billing_suspended_at timestamptz;
UPDATE user_apps
SET billing_suspended_at = now()
WHERE deleted_at IS NULL
  AND status = 'suspended'
  AND suspension_reason = 'billing_insufficient';

CREATE INDEX user_apps_billing_suspended_cleanup_idx
    ON user_apps(billing_suspended_at, id)
    WHERE deleted_at IS NULL
      AND status = 'suspended'
      AND suspension_reason = 'billing_insufficient';

ALTER TABLE user_notifications DROP CONSTRAINT user_notifications_kind_check;
ALTER TABLE user_notifications ADD CONSTRAINT user_notifications_kind_check CHECK (kind IN (
    'low_balance','billing_suspended','billing_recovered','billing_app_deleted',
    'subscription_purchased','subscription_purchase_failed',
    'subscription_expiring','subscription_grace','subscription_expired'
));

COMMIT;
