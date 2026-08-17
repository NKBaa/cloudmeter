BEGIN;

ALTER TABLE user_subscriptions
    ADD COLUMN grace_ends_at timestamptz;

ALTER TABLE user_subscriptions
    DROP CONSTRAINT user_subscriptions_status_check;

ALTER TABLE user_subscriptions
    ADD CONSTRAINT user_subscriptions_status_check
    CHECK (status IN ('active','grace_period','canceled','expired'));

COMMIT;
