BEGIN;

ALTER TABLE user_subscriptions
    ADD COLUMN entitlements_snapshot jsonb,
    ADD COLUMN cycle_price_cents_snapshot bigint;

UPDATE user_subscriptions us
SET entitlements_snapshot = pv.entitlements,
    cycle_price_cents_snapshot = pv.cycle_price_cents
FROM plan_versions pv
WHERE pv.id = us.plan_version_id;

ALTER TABLE user_subscriptions
    ALTER COLUMN entitlements_snapshot SET NOT NULL,
    ALTER COLUMN cycle_price_cents_snapshot SET NOT NULL,
    ADD CONSTRAINT user_subscriptions_cycle_price_snapshot_check CHECK (cycle_price_cents_snapshot >= 0);

CREATE OR REPLACE FUNCTION assign_default_subscription() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    INSERT INTO user_subscriptions(user_id,plan_version_id,entitlements_snapshot,cycle_price_cents_snapshot)
    SELECT NEW.id,pv.id,pv.entitlements,pv.cycle_price_cents
    FROM plans p JOIN LATERAL (SELECT id,entitlements,cycle_price_cents FROM plan_versions WHERE plan_id=p.id AND effective_at<=now() ORDER BY effective_at DESC,version DESC LIMIT 1) pv ON true
    WHERE p.code='free' ON CONFLICT DO NOTHING;
    RETURN NEW;
END $$;

COMMIT;
