BEGIN;

UPDATE user_subscriptions
SET status = 'expired'
WHERE status = 'grace_period';

ALTER TABLE user_subscriptions
    DROP CONSTRAINT user_subscriptions_status_check;

ALTER TABLE user_subscriptions
    ADD CONSTRAINT user_subscriptions_status_check
    CHECK (status IN ('active','canceled','expired'));

ALTER TABLE user_subscriptions
    DROP COLUMN grace_ends_at;

COMMIT;
