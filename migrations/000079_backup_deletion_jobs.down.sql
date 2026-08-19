BEGIN;
DROP INDEX IF EXISTS app_backups_active_app_uidx;

-- Restore the parentage function used before 000079 changed it to validate
-- editable runtime selections. This keeps a rollback executable while the
-- trigger itself remains installed by migration 000066.
CREATE OR REPLACE FUNCTION enforce_app_release_parentage() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
    expected_product_slug text;
    expected_image_digest text;
    expected_runtime_spec jsonb;
    expected_route_spec jsonb;
    expected_health_spec jsonb;
    expected_update_spec jsonb;
    release_runtime_spec jsonb;
    declared_key text;
    declared_version_id text;
    secret_versions jsonb;
    cloned_snapshot boolean;
BEGIN
    SELECT product.slug, version.image_digest, version.runtime_spec,
           version.route_spec, version.health_spec, version.update_spec
    INTO expected_product_slug, expected_image_digest, expected_runtime_spec,
         expected_route_spec, expected_health_spec, expected_update_spec
    FROM user_apps app
    JOIN app_products product ON product.id = app.product_id
    JOIN app_product_versions version
      ON version.id = NEW.product_version_id
     AND version.product_id = app.product_id
     AND version.published_at IS NOT NULL
    WHERE app.id = NEW.user_app_id;

    IF NOT FOUND THEN
        RAISE EXCEPTION 'application release product version must belong to the application product';
    END IF;
    IF TG_OP = 'INSERT' AND NEW.state <> 'created' THEN
        RAISE EXCEPTION 'application releases must be inserted in created state';
    END IF;
    IF jsonb_typeof(NEW.immutable_snapshot) IS DISTINCT FROM 'object' THEN
        RAISE EXCEPTION 'application release snapshot must be an object';
    END IF;
    IF EXISTS (
        SELECT 1 FROM jsonb_object_keys(NEW.immutable_snapshot) AS field(name)
        WHERE name <> ALL(ARRAY['product_slug','image_digest','runtime_spec','route_spec','health_spec','update_spec','secret_versions'])
    ) THEN
        RAISE EXCEPTION 'application release snapshot contains an unknown field';
    END IF;
    IF TG_OP = 'UPDATE' THEN
        RETURN NEW;
    END IF;

    SELECT EXISTS (
        SELECT 1 FROM app_releases existing
        WHERE existing.user_app_id = NEW.user_app_id
          AND existing.product_version_id = NEW.product_version_id
          AND existing.immutable_snapshot = NEW.immutable_snapshot
    ) INTO cloned_snapshot;

    -- Versions created before the resource entitlement migrations gained
    -- bounded defaults in-place. Apply those same defaults to historical
    -- Release snapshots before comparing their executable configuration. A
    -- rollback/start may clone an older immutable snapshot whose product
    -- version was normalized in-place before version immutability existed;
    -- preserve that exact historical snapshot instead of silently rewriting it.
    IF NOT cloned_snapshot AND (
        NEW.immutable_snapshot->>'product_slug' IS DISTINCT FROM expected_product_slug
        OR NEW.immutable_snapshot->>'image_digest' IS DISTINCT FROM expected_image_digest
        OR normalize_release_runtime_spec(NEW.immutable_snapshot->'runtime_spec')
           IS DISTINCT FROM expected_runtime_spec
        OR NEW.immutable_snapshot->'route_spec' IS DISTINCT FROM expected_route_spec
        OR NEW.immutable_snapshot->'health_spec' IS DISTINCT FROM expected_health_spec
        OR coalesce(NEW.immutable_snapshot->'update_spec', '{"dataPolicy":"volume_compatible"}'::jsonb)
           IS DISTINCT FROM expected_update_spec
    ) THEN
        RAISE EXCEPTION 'application release snapshot does not match immutable product version';
    END IF;

    release_runtime_spec := coalesce(NEW.immutable_snapshot->'runtime_spec', '{}'::jsonb);
    secret_versions := coalesce(NEW.immutable_snapshot->'secret_versions', '{}'::jsonb);
    IF jsonb_typeof(release_runtime_spec) IS DISTINCT FROM 'object'
       OR jsonb_typeof(secret_versions) IS DISTINCT FROM 'object'
       OR jsonb_typeof(coalesce(release_runtime_spec->'secretKeys', '[]'::jsonb)) IS DISTINCT FROM 'array' THEN
        RAISE EXCEPTION 'application release Secret references are invalid';
    END IF;
    IF NOT cloned_snapshot AND EXISTS (
        SELECT 1 FROM jsonb_object_keys(secret_versions) AS secret(key)
        WHERE NOT (coalesce(release_runtime_spec->'secretKeys', '[]'::jsonb) ? secret.key)
    ) THEN
        RAISE EXCEPTION 'application release contains an undeclared Secret reference';
    END IF;
    FOR declared_key IN
        SELECT jsonb_array_elements_text(coalesce(release_runtime_spec->'secretKeys', '[]'::jsonb))
    LOOP
        declared_version_id := coalesce(secret_versions->>declared_key, '');
        IF declared_version_id !~ '^[0-9a-f]{8}-[0-9a-f]{4}-[1-5][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$' THEN
            RAISE EXCEPTION 'application release Secret % must reference the same application and key', declared_key;
        END IF;
        IF NOT EXISTS (
            SELECT 1
            FROM app_secret_versions secret_version
            JOIN app_secrets secret ON secret.id = secret_version.app_secret_id
            WHERE secret_version.id = declared_version_id::uuid
              AND secret.user_app_id = NEW.user_app_id
              AND secret.key = declared_key
        ) THEN
            RAISE EXCEPTION 'application release Secret % must reference the same application and key', declared_key;
        END IF;
    END LOOP;
    RETURN NEW;
END $$;

DROP FUNCTION IF EXISTS normalize_selected_release_runtime;
DROP TRIGGER IF EXISTS app_backup_deletion_jobs_integrity_guard ON app_backup_deletion_jobs;
DROP FUNCTION IF EXISTS enforce_app_backup_deletion_job_integrity;
DROP TABLE IF EXISTS app_storage_metrics;
DROP TABLE IF EXISTS app_backup_deletion_jobs;
COMMIT;
