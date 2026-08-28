BEGIN;

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
    THEN
        RAISE EXCEPTION 'application route container does not match instance and release identity';
    END IF;
    RETURN NEW;
END $$;

UPDATE app_routes route
SET public_path = '/apps/'||users.slug||'/'||app.slug
FROM user_apps app JOIN users ON users.id=app.user_id
WHERE route.user_app_id=app.id;

DROP TABLE app_access_grants;
ALTER TABLE system_settings DROP COLUMN app_base_domain;

COMMIT;
