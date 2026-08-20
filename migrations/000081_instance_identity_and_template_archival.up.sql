BEGIN;

ALTER TABLE user_apps ADD COLUMN instance_id uuid;
UPDATE user_apps SET instance_id = gen_random_uuid() WHERE instance_id IS NULL;
ALTER TABLE user_apps ALTER COLUMN instance_id SET DEFAULT gen_random_uuid();
ALTER TABLE user_apps ALTER COLUMN instance_id SET NOT NULL;
ALTER TABLE user_apps ADD CONSTRAINT user_apps_instance_id_key UNIQUE(instance_id);
ALTER TABLE user_apps ADD CONSTRAINT user_apps_instance_id_distinct_check CHECK(instance_id <> id);

ALTER TABLE app_routes ADD COLUMN instance_id uuid;
UPDATE app_routes route SET instance_id = app.instance_id FROM user_apps app WHERE app.id = route.user_app_id;
ALTER TABLE app_routes ALTER COLUMN instance_id SET NOT NULL;
ALTER TABLE app_routes ADD CONSTRAINT app_routes_instance_id_key UNIQUE(instance_id);
ALTER TABLE user_apps ADD CONSTRAINT user_apps_id_instance_id_key UNIQUE(id, instance_id);
ALTER TABLE app_routes ADD CONSTRAINT app_routes_instance_parent_fk
    FOREIGN KEY(user_app_id, instance_id) REFERENCES user_apps(id, instance_id);

ALTER TABLE app_product_versions ADD COLUMN archived_at timestamptz;
CREATE INDEX app_product_versions_available_idx ON app_product_versions(product_id, version DESC) WHERE archived_at IS NULL;

CREATE OR REPLACE FUNCTION protect_user_app_identity() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP = 'UPDATE' AND (NEW.id <> OLD.id OR NEW.instance_id <> OLD.instance_id OR NEW.user_id <> OLD.user_id
       OR NEW.product_id <> OLD.product_id OR NEW.slug <> OLD.slug OR NEW.service_slug <> OLD.service_slug
       OR NEW.created_at <> OLD.created_at) THEN
        RAISE EXCEPTION 'user application identity is immutable';
    END IF;
    IF NEW.instance_id = NEW.id THEN RAISE EXCEPTION 'instance identity must be independent from application row identity'; END IF;
    IF NEW.last_successful_release_id IS NOT NULL AND NOT EXISTS (
        SELECT 1 FROM app_releases release WHERE release.id=NEW.last_successful_release_id AND release.user_app_id=NEW.id
    ) THEN RAISE EXCEPTION 'last successful release must belong to the same application'; END IF;
    RETURN NEW;
END $$;

CREATE OR REPLACE FUNCTION enforce_app_route_integrity() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE expected_path text; expected_host text; expected_port integer; expected_instance_id uuid;
        app_release_id uuid; app_status text; release_state text;
BEGIN
    SELECT '/apps/'||users.slug||'/'||app.slug, 'release-'||left(replace(release.id::text,'-',''),12),
           coalesce(nullif(release.immutable_snapshot->'route_spec'->>'port','')::integer,
                    nullif(release.immutable_snapshot->'route_spec'->>'containerPort','')::integer,8080),
           app.instance_id, app.last_successful_release_id, app.status, release.state
    INTO expected_path,expected_host,expected_port,expected_instance_id,app_release_id,app_status,release_state
    FROM user_apps app JOIN users ON users.id=app.user_id
    JOIN app_releases release ON release.id=NEW.release_id AND release.user_app_id=app.id WHERE app.id=NEW.user_app_id;
    IF NOT FOUND THEN RAISE EXCEPTION 'application route release must belong to the same application'; END IF;
    IF NEW.instance_id <> expected_instance_id THEN RAISE EXCEPTION 'application route instance identity mismatch'; END IF;
    IF app_status NOT IN ('running','updating') OR release_state<>'active' OR app_release_id IS DISTINCT FROM NEW.release_id THEN
        RAISE EXCEPTION 'application route must reference the active successful release'; END IF;
    IF NEW.public_path<>expected_path THEN RAISE EXCEPTION 'application route public path does not match application identity'; END IF;
    IF NEW.upstream_host<>expected_host THEN RAISE EXCEPTION 'application route upstream host does not match release identity'; END IF;
    IF NEW.upstream_port<>expected_port THEN RAISE EXCEPTION 'application route upstream port does not match release snapshot'; END IF;
    IF (NEW.upstream_container <> 'cm-'||expected_instance_id::text||'-'||NEW.release_id::text)
       AND (NEW.upstream_container !~ ('^cm-[0-9a-f]{10}-'||expected_instance_id::text||'-'||NEW.release_id::text||'$'))
       AND (NEW.upstream_container <> 'cm-'||NEW.user_app_id::text||'-'||NEW.release_id::text)
       AND (NEW.upstream_container !~ ('^cm-[0-9a-f]{10}-'||NEW.user_app_id::text||'-'||NEW.release_id::text||'$')) THEN
        RAISE EXCEPTION 'application route container does not match instance and release identity'; END IF;
    RETURN NEW;
END $$;

COMMIT;
