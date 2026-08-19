BEGIN;

-- Backup rows are immutable history. Physical archive cleanup is represented by
-- a separate retryable job so an object-store/Docker failure never erases the
-- audit trail or makes a restore reference dangling.
CREATE TABLE app_backup_deletion_jobs (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    backup_id uuid NOT NULL UNIQUE REFERENCES app_backups(id),
    requested_by uuid NOT NULL REFERENCES users(id),
    status text NOT NULL DEFAULT 'queued' CHECK (status IN ('queued','running','succeeded','failed')),
    attempts integer NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    available_at timestamptz NOT NULL DEFAULT now(),
    last_error text,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    completed_at timestamptz,
    CHECK ((status IN ('queued','running') AND completed_at IS NULL)
        OR (status IN ('succeeded','failed') AND completed_at IS NOT NULL))
);
CREATE INDEX app_backup_deletion_jobs_queue_idx
    ON app_backup_deletion_jobs(status, available_at, created_at);
CREATE INDEX app_backup_deletion_jobs_backup_idx
    ON app_backup_deletion_jobs(backup_id, status);
CREATE UNIQUE INDEX app_backups_active_app_uidx
    ON app_backups(user_app_id)
    WHERE status IN ('queued','running');

-- The API has no Docker socket. The worker publishes the latest observed size
-- of every application volume so the console can show how the one shared
-- capacity pool is divided between live data and retained backups.
CREATE TABLE app_storage_metrics (
    user_app_id uuid NOT NULL REFERENCES user_apps(id) ON DELETE CASCADE,
    volume_key text NOT NULL,
    usage_bytes bigint NOT NULL DEFAULT 0 CHECK (usage_bytes >= 0),
    sampled_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (user_app_id, volume_key)
);
CREATE INDEX app_storage_metrics_sampled_idx ON app_storage_metrics(sampled_at DESC);

-- A deletion request is intentionally append-only. Failed jobs can be retried
-- by inserting no second row: the API changes only the queue state.
CREATE FUNCTION enforce_app_backup_deletion_job_integrity() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP = 'UPDATE' AND (NEW.id <> OLD.id OR NEW.backup_id <> OLD.backup_id
        OR NEW.requested_by <> OLD.requested_by OR NEW.created_at <> OLD.created_at) THEN
        RAISE EXCEPTION 'backup deletion job identity is immutable';
    END IF;
    IF TG_OP = 'UPDATE' AND NEW.status <> OLD.status AND NOT (
        (OLD.status = 'queued' AND NEW.status IN ('running','failed'))
        OR (OLD.status = 'running' AND NEW.status IN ('queued','succeeded','failed'))
        OR (OLD.status = 'failed' AND NEW.status = 'queued')
    ) THEN
        RAISE EXCEPTION 'invalid backup deletion transition: % -> %', OLD.status, NEW.status;
    END IF;
    RETURN NEW;
END $$;
CREATE TRIGGER app_backup_deletion_jobs_integrity_guard
BEFORE UPDATE ON app_backup_deletion_jobs
FOR EACH ROW EXECUTE FUNCTION enforce_app_backup_deletion_job_integrity();

-- Release snapshots may differ from the immutable product version only in
-- options explicitly exposed by the administrator. This function normalizes
-- those selected values back to the template before the existing parentage
-- guard compares the executable configuration. It fails closed for malformed,
-- below-minimum, or undeclared overrides.
CREATE FUNCTION normalize_selected_release_runtime(spec jsonb, template jsonb) RETURNS jsonb
LANGUAGE plpgsql IMMUTABLE AS $$
DECLARE
    normalized jsonb := normalize_release_runtime_spec(spec);
    expected jsonb := normalize_release_runtime_spec(template);
    options jsonb := coalesce(template->'editableOptions','{}'::jsonb);
    release_env jsonb := coalesce(normalized->'env','{}'::jsonb);
    template_env jsonb := coalesce(expected->'env','{}'::jsonb);
    editable_env jsonb := coalesce(template->'editableEnvKeys','[]'::jsonb);
    env_key text;
    release_volume jsonb;
    expected_volume jsonb;
    release_volumes jsonb;
BEGIN
    IF normalized IS NULL OR expected IS NULL OR jsonb_typeof(options) IS DISTINCT FROM 'object'
       OR jsonb_typeof(release_env) IS DISTINCT FROM 'object'
       OR jsonb_typeof(editable_env) IS DISTINCT FROM 'array' THEN
        RETURN NULL;
    END IF;
    IF options->>'cpu' = 'true' THEN
        IF jsonb_typeof(normalized->'cpuCores') <> 'number'
           OR (normalized->>'cpuCores')::numeric < (expected->>'cpuCores')::numeric THEN RETURN NULL; END IF;
        normalized := jsonb_set(normalized,'{cpuCores}',expected->'cpuCores',true);
    END IF;
    IF options->>'memory' = 'true' THEN
        IF jsonb_typeof(normalized->'memoryMiB') <> 'number'
           OR (normalized->>'memoryMiB')::numeric < (expected->>'memoryMiB')::numeric THEN RETURN NULL; END IF;
        normalized := jsonb_set(normalized,'{memoryMiB}',expected->'memoryMiB',true);
    END IF;
    IF options->>'dataVolume' = 'true' THEN
        IF jsonb_array_length(coalesce(expected->'volumes','[]'::jsonb)) = 0 THEN
            IF normalized ? 'dataVolumeGiB' THEN RETURN NULL; END IF;
        ELSE
            IF jsonb_typeof(normalized->'dataVolumeGiB') <> 'number'
               OR (normalized->>'dataVolumeGiB')::numeric < (expected->>'dataVolumeGiB')::numeric THEN RETURN NULL; END IF;
            release_volumes := '[]'::jsonb;
            FOR release_volume IN SELECT value FROM jsonb_array_elements(normalized->'volumes') LOOP
                SELECT value INTO expected_volume FROM jsonb_array_elements(expected->'volumes') WHERE value->>'name'=release_volume->>'name';
                IF expected_volume IS NULL OR release_volume->>'mountPath' IS DISTINCT FROM expected_volume->>'mountPath'
                   OR jsonb_typeof(release_volume->'sizeGiB') <> 'number'
                   OR (release_volume->>'sizeGiB')::numeric <> (normalized->>'dataVolumeGiB')::numeric THEN RETURN NULL; END IF;
                release_volumes := release_volumes || jsonb_build_array(jsonb_set(release_volume,'{sizeGiB}',expected_volume->'sizeGiB',true));
                expected_volume := NULL;
            END LOOP;
            normalized := jsonb_set(normalized,'{volumes}',release_volumes,true);
            normalized := jsonb_set(normalized,'{dataVolumeGiB}',expected->'dataVolumeGiB',true);
        END IF;
    END IF;
    IF options->>'command' = 'true' THEN
        IF normalized ? 'command' AND jsonb_typeof(normalized->'command') <> 'array' THEN RETURN NULL; END IF;
        IF expected ? 'command' THEN normalized := jsonb_set(normalized,'{command}',expected->'command',true);
        ELSE normalized := normalized - 'command'; END IF;
    END IF;
    FOR env_key IN SELECT jsonb_array_elements_text(editable_env) LOOP
        IF NOT (release_env ? env_key) OR jsonb_typeof(release_env->env_key) <> 'string' THEN RETURN NULL; END IF;
        template_env := jsonb_set(template_env,ARRAY[env_key],release_env->env_key,true);
    END LOOP;
    normalized := jsonb_set(normalized,'{env}',template_env,true);
    IF options->>'dependencies' = 'true' THEN
        IF normalized ? 'dependencies' AND jsonb_typeof(normalized->'dependencies') <> 'array' THEN RETURN NULL; END IF;
        IF expected ? 'dependencies' THEN normalized := jsonb_set(normalized,'{dependencies}',expected->'dependencies',true);
        ELSE normalized := normalized - 'dependencies'; END IF;
    END IF;
    RETURN normalized;
EXCEPTION WHEN others THEN
    RETURN NULL;
END $$;

CREATE OR REPLACE FUNCTION enforce_app_release_parentage() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
    expected_product_slug text; expected_image_digest text; expected_runtime_spec jsonb; expected_route_spec jsonb;
    expected_health_spec jsonb; expected_update_spec jsonb; release_runtime_spec jsonb; declared_key text;
    declared_version_id text; secret_versions jsonb; cloned_snapshot boolean;
BEGIN
    SELECT product.slug,version.image_digest,version.runtime_spec,version.route_spec,version.health_spec,version.update_spec
    INTO expected_product_slug,expected_image_digest,expected_runtime_spec,expected_route_spec,expected_health_spec,expected_update_spec
    FROM user_apps app JOIN app_products product ON product.id=app.product_id
    JOIN app_product_versions version ON version.id=NEW.product_version_id AND version.product_id=app.product_id AND version.published_at IS NOT NULL
    WHERE app.id=NEW.user_app_id;
    IF NOT FOUND THEN RAISE EXCEPTION 'application release product version must belong to the application product'; END IF;
    IF TG_OP='INSERT' AND NEW.state<>'created' THEN RAISE EXCEPTION 'application releases must be inserted in created state'; END IF;
    IF jsonb_typeof(NEW.immutable_snapshot) IS DISTINCT FROM 'object' THEN RAISE EXCEPTION 'application release snapshot must be an object'; END IF;
    IF EXISTS (SELECT 1 FROM jsonb_object_keys(NEW.immutable_snapshot) field(name) WHERE name<>ALL(ARRAY['product_slug','image_digest','runtime_spec','route_spec','health_spec','update_spec','secret_versions'])) THEN RAISE EXCEPTION 'application release snapshot contains an unknown field'; END IF;
    IF TG_OP='UPDATE' THEN RETURN NEW; END IF;
    SELECT EXISTS(SELECT 1 FROM app_releases existing WHERE existing.user_app_id=NEW.user_app_id AND existing.product_version_id=NEW.product_version_id AND existing.immutable_snapshot=NEW.immutable_snapshot) INTO cloned_snapshot;
    IF NOT cloned_snapshot AND (NEW.immutable_snapshot->>'product_slug' IS DISTINCT FROM expected_product_slug
       OR NEW.immutable_snapshot->>'image_digest' IS DISTINCT FROM expected_image_digest
       OR normalize_selected_release_runtime(NEW.immutable_snapshot->'runtime_spec',expected_runtime_spec) IS DISTINCT FROM expected_runtime_spec
       OR NEW.immutable_snapshot->'route_spec' IS DISTINCT FROM expected_route_spec
       OR NEW.immutable_snapshot->'health_spec' IS DISTINCT FROM expected_health_spec
       OR coalesce(NEW.immutable_snapshot->'update_spec','{"dataPolicy":"volume_compatible"}'::jsonb) IS DISTINCT FROM expected_update_spec)
    THEN RAISE EXCEPTION 'application release snapshot does not match immutable product version'; END IF;
    release_runtime_spec:=coalesce(NEW.immutable_snapshot->'runtime_spec','{}'::jsonb); secret_versions:=coalesce(NEW.immutable_snapshot->'secret_versions','{}'::jsonb);
    IF jsonb_typeof(release_runtime_spec) IS DISTINCT FROM 'object' OR jsonb_typeof(secret_versions) IS DISTINCT FROM 'object' OR jsonb_typeof(coalesce(release_runtime_spec->'secretKeys','[]'::jsonb)) IS DISTINCT FROM 'array' THEN RAISE EXCEPTION 'application release Secret references are invalid'; END IF;
    IF NOT cloned_snapshot AND EXISTS(SELECT 1 FROM jsonb_object_keys(secret_versions) secret(key) WHERE NOT (coalesce(release_runtime_spec->'secretKeys','[]'::jsonb) ? secret.key)) THEN RAISE EXCEPTION 'application release contains an undeclared Secret reference'; END IF;
    FOR declared_key IN SELECT jsonb_array_elements_text(coalesce(release_runtime_spec->'secretKeys','[]'::jsonb)) LOOP
        declared_version_id:=coalesce(secret_versions->>declared_key,'');
        IF declared_version_id !~ '^[0-9a-f]{8}-[0-9a-f]{4}-[1-5][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$' OR NOT EXISTS(SELECT 1 FROM app_secret_versions version JOIN app_secrets secret ON secret.id=version.app_secret_id WHERE version.id=declared_version_id::uuid AND secret.user_app_id=NEW.user_app_id AND secret.key=declared_key) THEN RAISE EXCEPTION 'application release Secret % must reference the same application and key',declared_key; END IF;
    END LOOP;
    RETURN NEW;
END $$;

COMMIT;
