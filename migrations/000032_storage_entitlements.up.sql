BEGIN;

CREATE OR REPLACE FUNCTION runtime_storage_spec_valid(spec jsonb) RETURNS boolean
LANGUAGE plpgsql IMMUTABLE AS $$
DECLARE
    volume jsonb;
BEGIN
    IF jsonb_typeof(spec->'systemDiskGiB') IS DISTINCT FROM 'number'
       OR (spec->>'systemDiskGiB')::numeric < 1
       OR (spec->>'systemDiskGiB')::numeric > 1024 THEN
        RETURN false;
    END IF;
    IF spec ? 'volumes' AND jsonb_typeof(spec->'volumes') IS DISTINCT FROM 'array' THEN
        RETURN false;
    END IF;
    FOR volume IN SELECT value FROM jsonb_array_elements(coalesce(spec->'volumes','[]'::jsonb)) LOOP
        IF jsonb_typeof(volume) IS DISTINCT FROM 'object'
           OR jsonb_typeof(volume->'sizeGiB') IS DISTINCT FROM 'number'
           OR (volume->>'sizeGiB')::numeric < 1
           OR (volume->>'sizeGiB')::numeric > 16384 THEN
            RETURN false;
        END IF;
    END LOOP;
    RETURN true;
END $$;

UPDATE app_product_versions
SET runtime_spec = jsonb_set(
    jsonb_set(runtime_spec, '{systemDiskGiB}', coalesce(runtime_spec->'systemDiskGiB', '5'::jsonb), true),
    '{volumes}',
    coalesce((SELECT jsonb_agg(value || jsonb_build_object('sizeGiB',coalesce(value->'sizeGiB','10'::jsonb)))
              FROM jsonb_array_elements(coalesce(runtime_spec->'volumes','[]'::jsonb))), '[]'::jsonb),
    true
);

ALTER TABLE app_product_versions ADD CONSTRAINT app_product_versions_runtime_storage_check
    CHECK (runtime_storage_spec_valid(runtime_spec));

UPDATE plan_versions SET entitlements = entitlements
    || jsonb_build_object(
        'systemDiskGiB',coalesce((entitlements->>'systemDiskGiB')::numeric,coalesce((entitlements->>'apps')::numeric,1)*10),
        'dataDiskGiB',coalesce((entitlements->>'dataDiskGiB')::numeric,coalesce((entitlements->>'apps')::numeric,1)*20)
    );

UPDATE user_subscriptions SET entitlements_snapshot = entitlements_snapshot
    || jsonb_build_object(
        'systemDiskGiB',coalesce((entitlements_snapshot->>'systemDiskGiB')::numeric,coalesce((entitlements_snapshot->>'apps')::numeric,1)*10),
        'dataDiskGiB',coalesce((entitlements_snapshot->>'dataDiskGiB')::numeric,coalesce((entitlements_snapshot->>'apps')::numeric,1)*20)
    );

ALTER TABLE plan_versions ADD CONSTRAINT plan_versions_storage_entitlements_check CHECK (
    jsonb_typeof(entitlements->'systemDiskGiB')='number'
    AND (entitlements->>'systemDiskGiB')::numeric > 0
    AND jsonb_typeof(entitlements->'dataDiskGiB')='number'
    AND (entitlements->>'dataDiskGiB')::numeric >= 0
);
ALTER TABLE user_subscriptions ADD CONSTRAINT user_subscriptions_storage_snapshot_check CHECK (
    jsonb_typeof(entitlements_snapshot->'systemDiskGiB')='number'
    AND (entitlements_snapshot->>'systemDiskGiB')::numeric > 0
    AND jsonb_typeof(entitlements_snapshot->'dataDiskGiB')='number'
    AND (entitlements_snapshot->>'dataDiskGiB')::numeric >= 0
);

COMMIT;
