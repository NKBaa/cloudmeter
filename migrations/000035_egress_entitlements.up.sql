BEGIN;
UPDATE plan_versions SET entitlements = entitlements || jsonb_build_object(
    'egressGiB',coalesce((entitlements->>'egressGiB')::numeric,coalesce((entitlements->>'apps')::numeric,1)*10),
    'egressOverageEnabled',coalesce((entitlements->>'egressOverageEnabled')::boolean,false)
);
UPDATE user_subscriptions SET entitlements_snapshot = entitlements_snapshot || jsonb_build_object(
    'egressGiB',coalesce((entitlements_snapshot->>'egressGiB')::numeric,coalesce((entitlements_snapshot->>'apps')::numeric,1)*10),
    'egressOverageEnabled',coalesce((entitlements_snapshot->>'egressOverageEnabled')::boolean,false)
);
ALTER TABLE plan_versions ADD CONSTRAINT plan_versions_egress_entitlements_check CHECK (
    jsonb_typeof(entitlements->'egressGiB')='number' AND (entitlements->>'egressGiB')::numeric >= 0
    AND jsonb_typeof(entitlements->'egressOverageEnabled')='boolean'
);
ALTER TABLE user_subscriptions ADD CONSTRAINT user_subscriptions_egress_snapshot_check CHECK (
    jsonb_typeof(entitlements_snapshot->'egressGiB')='number' AND (entitlements_snapshot->>'egressGiB')::numeric >= 0
    AND jsonb_typeof(entitlements_snapshot->'egressOverageEnabled')='boolean'
);
ALTER TABLE user_apps DROP CONSTRAINT IF EXISTS user_apps_suspension_reason_check;
ALTER TABLE user_apps ADD CONSTRAINT user_apps_suspension_reason_check CHECK
 (suspension_reason IS NULL OR suspension_reason IN ('billing_insufficient','subscription_expired','egress_quota'));
COMMIT;
