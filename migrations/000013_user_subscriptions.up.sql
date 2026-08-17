BEGIN;
CREATE TABLE user_subscriptions (
    user_id uuid PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    plan_version_id uuid NOT NULL REFERENCES plan_versions(id),
    status text NOT NULL DEFAULT 'active' CHECK (status IN ('active','canceled','expired')),
    starts_at timestamptz NOT NULL DEFAULT now(),
    ends_at timestamptz,
    updated_at timestamptz NOT NULL DEFAULT now()
);
INSERT INTO user_subscriptions(user_id,plan_version_id)
SELECT u.id,pv.id FROM users u CROSS JOIN LATERAL (SELECT pv.id FROM plans p JOIN plan_versions pv ON pv.plan_id=p.id WHERE p.code='free' AND pv.effective_at<=now() ORDER BY pv.effective_at DESC,pv.version DESC LIMIT 1) pv;
CREATE FUNCTION assign_default_subscription() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    INSERT INTO user_subscriptions(user_id,plan_version_id) SELECT NEW.id,pv.id FROM plans p JOIN LATERAL (SELECT id FROM plan_versions WHERE plan_id=p.id AND effective_at<=now() ORDER BY effective_at DESC,version DESC LIMIT 1) pv ON true WHERE p.code='free' ON CONFLICT DO NOTHING;
    RETURN NEW;
END $$;
CREATE TRIGGER users_default_subscription AFTER INSERT ON users FOR EACH ROW EXECUTE FUNCTION assign_default_subscription();
COMMIT;
