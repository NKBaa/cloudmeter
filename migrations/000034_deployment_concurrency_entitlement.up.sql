BEGIN;
UPDATE plan_versions SET entitlements = entitlements || jsonb_build_object(
    'concurrentDeployments',coalesce((entitlements->>'concurrentDeployments')::int,least(greatest((entitlements->>'apps')::int,1),2))
);
UPDATE user_subscriptions SET entitlements_snapshot = entitlements_snapshot || jsonb_build_object(
    'concurrentDeployments',coalesce((entitlements_snapshot->>'concurrentDeployments')::int,least(greatest((entitlements_snapshot->>'apps')::int,1),2))
);
ALTER TABLE plan_versions ADD CONSTRAINT plan_versions_deployment_concurrency_check CHECK (
    jsonb_typeof(entitlements->'concurrentDeployments')='number'
    AND (entitlements->>'concurrentDeployments')::int BETWEEN 1 AND 1000
);
ALTER TABLE user_subscriptions ADD CONSTRAINT user_subscriptions_deployment_concurrency_snapshot_check CHECK (
    jsonb_typeof(entitlements_snapshot->'concurrentDeployments')='number'
    AND (entitlements_snapshot->>'concurrentDeployments')::int BETWEEN 1 AND 1000
);
COMMIT;
