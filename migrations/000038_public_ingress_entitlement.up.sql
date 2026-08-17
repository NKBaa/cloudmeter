BEGIN;
UPDATE plan_versions SET entitlements = entitlements || jsonb_build_object(
    'publicIngresses',coalesce((entitlements->>'publicIngresses')::int,coalesce((entitlements->>'apps')::int,1)),
    'ingressOverageEnabled',coalesce((entitlements->>'ingressOverageEnabled')::boolean,false)
);
UPDATE user_subscriptions SET entitlements_snapshot = entitlements_snapshot || jsonb_build_object(
    'publicIngresses',coalesce((entitlements_snapshot->>'publicIngresses')::int,coalesce((entitlements_snapshot->>'apps')::int,1)),
    'ingressOverageEnabled',coalesce((entitlements_snapshot->>'ingressOverageEnabled')::boolean,false)
);
ALTER TABLE plan_versions ADD CONSTRAINT plan_versions_public_ingress_check CHECK (
    jsonb_typeof(entitlements->'publicIngresses')='number' AND (entitlements->>'publicIngresses')::int >= 0
    AND jsonb_typeof(entitlements->'ingressOverageEnabled')='boolean'
);
ALTER TABLE user_subscriptions ADD CONSTRAINT user_subscriptions_public_ingress_check CHECK (
    jsonb_typeof(entitlements_snapshot->'publicIngresses')='number' AND (entitlements_snapshot->>'publicIngresses')::int >= 0
    AND jsonb_typeof(entitlements_snapshot->'ingressOverageEnabled')='boolean'
);
INSERT INTO pricing_items(code,unit) VALUES ('network.public_ingress','ingress') ON CONFLICT (code) DO NOTHING;
CREATE UNIQUE INDEX usage_events_public_ingress_app_uidx
    ON usage_events(user_app_id)
    WHERE usage_code='network.public_ingress' AND user_app_id IS NOT NULL;
COMMIT;
