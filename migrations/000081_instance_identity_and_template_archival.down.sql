BEGIN;
DROP TRIGGER IF EXISTS app_routes_integrity_guard ON app_routes;
DROP TRIGGER IF EXISTS user_apps_identity_immutable ON user_apps;
DROP INDEX IF EXISTS app_product_versions_available_idx;
ALTER TABLE app_product_versions DROP COLUMN IF EXISTS archived_at;
ALTER TABLE app_routes DROP CONSTRAINT IF EXISTS app_routes_instance_parent_fk;
ALTER TABLE app_routes DROP CONSTRAINT IF EXISTS app_routes_instance_id_key;
ALTER TABLE app_routes DROP COLUMN IF EXISTS instance_id;
ALTER TABLE user_apps DROP CONSTRAINT IF EXISTS user_apps_id_instance_id_key;
ALTER TABLE user_apps DROP CONSTRAINT IF EXISTS user_apps_instance_id_distinct_check;
ALTER TABLE user_apps DROP CONSTRAINT IF EXISTS user_apps_instance_id_key;
ALTER TABLE user_apps DROP COLUMN IF EXISTS instance_id;

CREATE OR REPLACE FUNCTION protect_user_app_identity() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP = 'UPDATE' AND (NEW.id <> OLD.id OR NEW.user_id <> OLD.user_id OR NEW.product_id <> OLD.product_id
       OR NEW.slug <> OLD.slug OR NEW.service_slug <> OLD.service_slug OR NEW.created_at <> OLD.created_at) THEN
        RAISE EXCEPTION 'user application identity is immutable';
    END IF;
    IF NEW.last_successful_release_id IS NOT NULL AND NOT EXISTS (
        SELECT 1 FROM app_releases release WHERE release.id=NEW.last_successful_release_id AND release.user_app_id=NEW.id
    ) THEN RAISE EXCEPTION 'last successful release must belong to the same application'; END IF;
    RETURN NEW;
END $$;

CREATE OR REPLACE FUNCTION enforce_app_route_integrity() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE expected_path text; expected_host text; expected_port integer;
        app_release_id uuid; app_status text; release_state text;
BEGIN
    SELECT '/apps/'||users.slug||'/'||app.slug, 'release-'||left(replace(release.id::text,'-',''),12),
           coalesce(nullif(release.immutable_snapshot->'route_spec'->>'port','')::integer,
                    nullif(release.immutable_snapshot->'route_spec'->>'containerPort','')::integer,8080),
           app.last_successful_release_id,app.status,release.state
    INTO expected_path,expected_host,expected_port,app_release_id,app_status,release_state
    FROM user_apps app JOIN users ON users.id=app.user_id
    JOIN app_releases release ON release.id=NEW.release_id AND release.user_app_id=app.id WHERE app.id=NEW.user_app_id;
    IF NOT FOUND THEN RAISE EXCEPTION 'application route release must belong to the same application'; END IF;
    IF app_status NOT IN ('running','updating') OR release_state<>'active' OR app_release_id IS DISTINCT FROM NEW.release_id THEN
        RAISE EXCEPTION 'application route must reference the active successful release'; END IF;
    IF NEW.public_path<>expected_path THEN RAISE EXCEPTION 'application route public path does not match application identity'; END IF;
    IF NEW.upstream_host<>expected_host THEN RAISE EXCEPTION 'application route upstream host does not match release identity'; END IF;
    IF NEW.upstream_port<>expected_port THEN RAISE EXCEPTION 'application route upstream port does not match release snapshot'; END IF;
    IF (NEW.upstream_container <> 'cm-'||NEW.user_app_id::text||'-'||NEW.release_id::text)
       AND (NEW.upstream_container !~ ('^cm-[0-9a-f]{10}-'||NEW.user_app_id::text||'-'||NEW.release_id::text||'$')) THEN
        RAISE EXCEPTION 'application route container does not match application and release identity'; END IF;
    RETURN NEW;
END $$;

CREATE TRIGGER user_apps_identity_immutable BEFORE INSERT OR UPDATE ON user_apps
FOR EACH ROW EXECUTE FUNCTION protect_user_app_identity();
CREATE TRIGGER app_routes_integrity_guard BEFORE INSERT OR UPDATE ON app_routes
FOR EACH ROW EXECUTE FUNCTION enforce_app_route_integrity();
COMMIT;
