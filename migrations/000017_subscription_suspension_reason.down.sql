BEGIN;

UPDATE user_apps
SET suspension_reason = NULL
WHERE suspension_reason = 'subscription_expired';

ALTER TABLE user_apps
    DROP CONSTRAINT user_apps_suspension_reason_check;

ALTER TABLE user_apps
    ADD CONSTRAINT user_apps_suspension_reason_check
    CHECK (suspension_reason IS NULL OR suspension_reason IN ('billing_insufficient'));

COMMIT;
