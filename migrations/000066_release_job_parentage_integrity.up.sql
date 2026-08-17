BEGIN;

ALTER TABLE deployment_jobs
    ADD CONSTRAINT deployment_jobs_idempotency_key_length_check
    CHECK (length(idempotency_key) BETWEEN 1 AND 128),
    ADD CONSTRAINT deployment_jobs_attempts_nonnegative_check
    CHECK (attempts >= 0);

CREATE UNIQUE INDEX deployment_jobs_active_app_uidx
    ON deployment_jobs(user_app_id)
    WHERE state NOT IN ('succeeded', 'failed');

CREATE FUNCTION normalize_release_runtime_spec(spec jsonb) RETURNS jsonb
LANGUAGE plpgsql IMMUTABLE AS $$
DECLARE
    normalized jsonb;
    normalized_volumes jsonb;
BEGIN
    IF jsonb_typeof(spec) IS DISTINCT FROM 'object' THEN
        RETURN NULL;
    END IF;
    normalized := jsonb_set(
        jsonb_set(spec, '{cpuCores}', coalesce(spec->'cpuCores', '1'::jsonb), true),
        '{memoryMiB}', coalesce(spec->'memoryMiB', '512'::jsonb), true
    );
    normalized := jsonb_set(
        normalized, '{systemDiskGiB}',
        coalesce(normalized->'systemDiskGiB', '5'::jsonb), true
    );
    IF normalized ? 'volumes'
       AND jsonb_typeof(normalized->'volumes') IS DISTINCT FROM 'array' THEN
        RETURN NULL;
    END IF;
    SELECT coalesce(
        jsonb_agg(value || jsonb_build_object(
            'sizeGiB', coalesce(value->'sizeGiB', '10'::jsonb)
        )),
        '[]'::jsonb
    )
    INTO normalized_volumes
    FROM jsonb_array_elements(coalesce(normalized->'volumes', '[]'::jsonb));
    RETURN jsonb_set(normalized, '{volumes}', normalized_volumes, true);
END $$;

CREATE FUNCTION enforce_app_release_parentage() RETURNS trigger LANGUAGE plpgsql AS $$
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

CREATE FUNCTION enforce_deployment_job_integrity() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
    target_state text;
    source_state text;
BEGIN
    IF TG_OP = 'UPDATE'
       AND (NEW.id <> OLD.id
            OR NEW.user_app_id <> OLD.user_app_id
            OR NEW.release_id <> OLD.release_id
            OR NEW.idempotency_key <> OLD.idempotency_key
            OR NEW.operation <> OLD.operation
            OR NEW.source_release_id IS DISTINCT FROM OLD.source_release_id
            OR NEW.created_at <> OLD.created_at) THEN
        RAISE EXCEPTION 'deployment job identity is immutable';
    END IF;
    IF TG_OP = 'UPDATE' AND OLD.state IN ('succeeded', 'failed')
       AND NEW IS DISTINCT FROM OLD THEN
        RAISE EXCEPTION 'completed deployment jobs are immutable';
    END IF;

    SELECT release.state INTO target_state
        FROM app_releases release
        WHERE release.id = NEW.release_id
          AND release.user_app_id = NEW.user_app_id;
    IF NOT FOUND THEN
        RAISE EXCEPTION 'deployment job release must belong to the same application';
    END IF;
    IF NEW.source_release_id IS NOT NULL
       THEN
        SELECT source.state INTO source_state
        FROM app_releases source
           WHERE source.id = NEW.source_release_id
             AND source.user_app_id = NEW.user_app_id;
        IF NOT FOUND THEN
            RAISE EXCEPTION 'deployment job source release must belong to the same application';
        END IF;
    END IF;

    IF TG_OP = 'UPDATE' THEN
        IF NEW.state <> OLD.state AND NOT (
            (OLD.state = 'queued' AND NEW.state = 'pulling')
            OR (OLD.state = 'pulling' AND NEW.state IN ('starting', 'rolling_back'))
            OR (OLD.state = 'starting' AND NEW.state IN ('health_checking', 'rolling_back'))
            OR (OLD.state = 'health_checking' AND NEW.state IN ('switching_route', 'rolling_back'))
            OR (OLD.state = 'switching_route' AND NEW.state IN ('succeeded', 'rolling_back'))
            OR (OLD.state = 'rolling_back' AND NEW.state = 'failed')
            OR (OLD.state NOT IN ('succeeded', 'failed') AND NEW.state = 'failed')
        ) THEN
            RAISE EXCEPTION 'invalid deployment job transition: % -> %', OLD.state, NEW.state;
        END IF;
        RETURN NEW;
    END IF;
    IF NEW.state <> 'queued' OR NEW.attempts <> 0 OR NEW.last_error IS NOT NULL THEN
        RAISE EXCEPTION 'deployment jobs must be inserted in a clean queued state';
    END IF;
    IF NEW.operation IN ('deploy', 'update') AND NEW.source_release_id IS NOT NULL THEN
        RAISE EXCEPTION 'deploy and update jobs cannot reference a source release';
    END IF;
    IF NEW.operation IN ('rollback', 'start')
       AND (NEW.source_release_id IS NULL OR NEW.source_release_id = NEW.release_id) THEN
        RAISE EXCEPTION 'rollback and start jobs require a distinct source release';
    END IF;
    IF NEW.operation IN ('billing_recovery', 'subscription_recovery')
       AND NEW.source_release_id IS DISTINCT FROM NEW.release_id THEN
        RAISE EXCEPTION 'recovery jobs must reuse their source release';
    END IF;
    IF NEW.operation IN ('deploy', 'update', 'rollback', 'start') AND target_state <> 'created' THEN
        RAISE EXCEPTION 'deployment target release must be in created state';
    END IF;
    IF NEW.operation IN ('rollback', 'start') AND source_state NOT IN ('active', 'superseded') THEN
        RAISE EXCEPTION 'rollback and start source releases must be successful';
    END IF;
    IF NEW.operation IN ('billing_recovery', 'subscription_recovery') AND target_state <> 'active' THEN
        RAISE EXCEPTION 'recovery jobs must reuse the active successful release';
    END IF;
    RETURN NEW;
END $$;

CREATE FUNCTION enforce_app_stop_job_integrity() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP = 'UPDATE'
       AND (NEW.id <> OLD.id
            OR NEW.user_app_id <> OLD.user_app_id
            OR NEW.release_id IS DISTINCT FROM OLD.release_id
            OR NEW.container_name <> OLD.container_name
            OR NEW.idempotency_key <> OLD.idempotency_key
            OR NEW.created_at <> OLD.created_at) THEN
        RAISE EXCEPTION 'application stop job identity is immutable';
    END IF;
    IF TG_OP = 'UPDATE' AND OLD.status = 'succeeded'
       AND NEW IS DISTINCT FROM OLD THEN
        RAISE EXCEPTION 'completed application stop jobs are immutable';
    END IF;
    IF NEW.release_id IS NOT NULL
       AND NOT EXISTS (
           SELECT 1 FROM app_releases release
           WHERE release.id = NEW.release_id
             AND release.user_app_id = NEW.user_app_id
       ) THEN
        RAISE EXCEPTION 'application stop job release must belong to the same application';
    END IF;
    IF NEW.container_name <> ''
       AND (NEW.release_id IS NULL OR NOT (
           NEW.container_name = 'cm-' || NEW.user_app_id::text || '-' || NEW.release_id::text
           OR NEW.container_name ~ ('^cm-[0-9a-f]{10}-' || NEW.user_app_id::text || '-' || NEW.release_id::text || '$')
       )) THEN
        RAISE EXCEPTION 'application stop job container must match its application and release';
    END IF;
    IF TG_OP = 'INSERT' AND (NEW.status <> 'queued' OR NEW.attempts <> 0 OR NEW.last_error IS NOT NULL) THEN
        RAISE EXCEPTION 'application stop jobs must be inserted in a clean queued state';
    END IF;
    IF TG_OP = 'UPDATE' AND NEW.status <> OLD.status AND NOT (
        (OLD.status = 'queued' AND NEW.status = 'running')
        OR (OLD.status = 'running' AND NEW.status IN ('queued', 'succeeded'))
    ) THEN
        RAISE EXCEPTION 'invalid application stop job transition: % -> %', OLD.status, NEW.status;
    END IF;
    RETURN NEW;
END $$;

CREATE FUNCTION deny_deployment_event_mutation() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'deployment events are immutable';
END $$;

CREATE TRIGGER app_releases_parentage_guard
BEFORE INSERT OR UPDATE ON app_releases
FOR EACH ROW EXECUTE FUNCTION enforce_app_release_parentage();

CREATE TRIGGER deployment_jobs_integrity_guard
BEFORE INSERT OR UPDATE ON deployment_jobs
FOR EACH ROW EXECUTE FUNCTION enforce_deployment_job_integrity();

CREATE TRIGGER app_stop_jobs_integrity_guard
BEFORE INSERT OR UPDATE ON app_stop_jobs
FOR EACH ROW EXECUTE FUNCTION enforce_app_stop_job_integrity();

CREATE TRIGGER deployment_jobs_no_delete
BEFORE DELETE ON deployment_jobs
FOR EACH ROW EXECUTE FUNCTION deny_immutable_history_delete();

CREATE TRIGGER deployment_jobs_no_truncate
BEFORE TRUNCATE ON deployment_jobs
FOR EACH STATEMENT EXECUTE FUNCTION deny_immutable_history_truncate();

CREATE TRIGGER deployment_events_no_update_delete
BEFORE UPDATE OR DELETE ON deployment_events
FOR EACH ROW EXECUTE FUNCTION deny_deployment_event_mutation();

CREATE TRIGGER deployment_events_no_truncate
BEFORE TRUNCATE ON deployment_events
FOR EACH STATEMENT EXECUTE FUNCTION deny_immutable_history_truncate();

CREATE TRIGGER app_stop_jobs_no_delete
BEFORE DELETE ON app_stop_jobs
FOR EACH ROW EXECUTE FUNCTION deny_immutable_history_delete();

CREATE TRIGGER app_stop_jobs_no_truncate
BEFORE TRUNCATE ON app_stop_jobs
FOR EACH STATEMENT EXECUTE FUNCTION deny_immutable_history_truncate();

-- Fail the upgrade instead of preserving any parent/child mismatch created by
-- an older direct database write.
UPDATE app_releases SET state = state;
UPDATE deployment_jobs SET updated_at = updated_at;
UPDATE app_stop_jobs SET updated_at = updated_at;

COMMIT;
