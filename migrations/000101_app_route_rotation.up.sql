BEGIN;

ALTER TABLE user_apps
    ADD COLUMN route_host_label text,
    ADD COLUMN domain_refresh_days integer,
    ADD COLUMN domain_refreshed_at timestamptz NOT NULL DEFAULT now(),
    ADD COLUMN domain_next_refresh_at timestamptz;

UPDATE user_apps app
SET route_host_label = CASE
    WHEN length(app.slug || '-' || users.slug) <= 63 THEN app.slug || '-' || users.slug
    ELSE left(trim(trailing '-' FROM app.slug), 50) || '-' || left(md5(app.id::text), 12)
END
FROM users
WHERE users.id = app.user_id;

ALTER TABLE user_apps
    ALTER COLUMN route_host_label SET NOT NULL,
    ADD CONSTRAINT user_apps_route_host_label_check
        CHECK (route_host_label ~ '^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$'),
    ADD CONSTRAINT user_apps_domain_refresh_days_check
        CHECK (domain_refresh_days IS NULL OR domain_refresh_days >= 1),
    ADD CONSTRAINT user_apps_domain_next_refresh_check
        CHECK ((domain_refresh_days IS NULL AND domain_next_refresh_at IS NULL)
            OR (domain_refresh_days IS NOT NULL AND domain_next_refresh_at IS NOT NULL));

CREATE UNIQUE INDEX user_apps_active_route_host_label_uidx
    ON user_apps(lower(route_host_label)) WHERE deleted_at IS NULL;
CREATE INDEX user_apps_domain_refresh_due_idx
    ON user_apps(domain_next_refresh_at)
    WHERE deleted_at IS NULL AND domain_next_refresh_at IS NOT NULL;

CREATE OR REPLACE FUNCTION enforce_app_route_integrity() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE expected_path text; expected_host text; expected_port integer; expected_instance_id uuid;
        app_release_id uuid; app_status text; release_state text; base_domain text;
BEGIN
    SELECT coalesce(settings.app_base_domain, '') INTO base_domain
    FROM system_settings settings WHERE settings.singleton;

    SELECT CASE
               WHEN base_domain = '' THEN '/apps/'||users.slug||'/'||app.slug
               ELSE '//'||app.route_host_label||'.'||base_domain||'/'
           END,
           'release-'||left(replace(release.id::text,'-',''),12),
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
SET public_path = CASE
    WHEN settings.app_base_domain = '' THEN '/apps/'||users.slug||'/'||app.slug
    ELSE '//'||app.route_host_label||'.'||settings.app_base_domain||'/'
END
FROM user_apps app
JOIN users ON users.id=app.user_id
CROSS JOIN system_settings settings
WHERE route.user_app_id=app.id AND settings.singleton;

COMMIT;
