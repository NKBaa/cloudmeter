BEGIN;

ALTER TABLE app_backups ADD COLUMN reserved_bytes bigint NOT NULL DEFAULT 0 CHECK (reserved_bytes >= 0);

UPDATE plan_versions SET entitlements = entitlements
    || jsonb_build_object(
        'backupStorageGiB',coalesce((entitlements->>'backupStorageGiB')::numeric,coalesce((entitlements->>'apps')::numeric,1)*20),
        'backupOperationsPerMonth',coalesce((entitlements->>'backupOperationsPerMonth')::int,coalesce((entitlements->>'apps')::int,1)*10)
    );
UPDATE user_subscriptions SET entitlements_snapshot = entitlements_snapshot
    || jsonb_build_object(
        'backupStorageGiB',coalesce((entitlements_snapshot->>'backupStorageGiB')::numeric,coalesce((entitlements_snapshot->>'apps')::numeric,1)*20),
        'backupOperationsPerMonth',coalesce((entitlements_snapshot->>'backupOperationsPerMonth')::int,coalesce((entitlements_snapshot->>'apps')::int,1)*10)
    );

ALTER TABLE plan_versions ADD CONSTRAINT plan_versions_backup_entitlements_check CHECK (
    jsonb_typeof(entitlements->'backupStorageGiB')='number'
    AND (entitlements->>'backupStorageGiB')::numeric >= 0
    AND jsonb_typeof(entitlements->'backupOperationsPerMonth')='number'
    AND (entitlements->>'backupOperationsPerMonth')::int >= 0
);
ALTER TABLE user_subscriptions ADD CONSTRAINT user_subscriptions_backup_snapshot_check CHECK (
    jsonb_typeof(entitlements_snapshot->'backupStorageGiB')='number'
    AND (entitlements_snapshot->>'backupStorageGiB')::numeric >= 0
    AND jsonb_typeof(entitlements_snapshot->'backupOperationsPerMonth')='number'
    AND (entitlements_snapshot->>'backupOperationsPerMonth')::int >= 0
);

COMMIT;
