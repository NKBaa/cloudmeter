BEGIN;

ALTER TABLE users
    ADD CONSTRAINT users_slug_format_check
    CHECK (slug ~ '^[a-z0-9][a-z0-9-]{0,62}$');

ALTER TABLE user_apps
    ADD CONSTRAINT user_apps_slug_format_check
    CHECK (slug ~ '^[a-z0-9][a-z0-9-]{0,62}$'),
    ADD CONSTRAINT user_apps_service_slug_format_check
    CHECK (service_slug ~ '^[a-z0-9][a-z0-9-]{0,62}$');

CREATE FUNCTION protect_user_public_identity() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.id <> OLD.id
       OR NEW.slug <> OLD.slug
       OR NEW.created_at <> OLD.created_at THEN
        RAISE EXCEPTION 'user public identity is immutable';
    END IF;
    RETURN NEW;
END $$;

CREATE FUNCTION protect_user_app_identity() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP = 'UPDATE'
       AND (NEW.id <> OLD.id
            OR NEW.user_id <> OLD.user_id
            OR NEW.product_id <> OLD.product_id
            OR NEW.slug <> OLD.slug
            OR NEW.service_slug <> OLD.service_slug
            OR NEW.created_at <> OLD.created_at) THEN
        RAISE EXCEPTION 'user application identity is immutable';
    END IF;
    IF NEW.last_successful_release_id IS NOT NULL
       AND NOT EXISTS (
           SELECT 1
           FROM app_releases release
           WHERE release.id = NEW.last_successful_release_id
             AND release.user_app_id = NEW.id
       ) THEN
        RAISE EXCEPTION 'last successful release must belong to the same application';
    END IF;
    RETURN NEW;
END $$;

CREATE FUNCTION enforce_app_route_integrity() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
    expected_path text;
    expected_host text;
    expected_port integer;
    app_release_id uuid;
    app_status text;
    release_state text;
BEGIN
    SELECT '/apps/' || users.slug || '/' || app.slug,
           'release-' || left(replace(release.id::text, '-', ''), 12),
           coalesce(
               nullif(release.immutable_snapshot->'route_spec'->>'port', '')::integer,
               nullif(release.immutable_snapshot->'route_spec'->>'containerPort', '')::integer,
               8080
           ),
           app.last_successful_release_id,
           app.status,
           release.state
    INTO expected_path, expected_host, expected_port, app_release_id, app_status, release_state
    FROM user_apps app
    JOIN users ON users.id = app.user_id
    JOIN app_releases release ON release.id = NEW.release_id AND release.user_app_id = app.id
    WHERE app.id = NEW.user_app_id;

    IF NOT FOUND THEN
        RAISE EXCEPTION 'application route release must belong to the same application';
    END IF;
    IF app_status NOT IN ('running', 'updating') OR release_state <> 'active' OR app_release_id IS DISTINCT FROM NEW.release_id THEN
        RAISE EXCEPTION 'application route must reference the active successful release';
    END IF;
    IF NEW.public_path <> expected_path THEN
        RAISE EXCEPTION 'application route public path does not match application identity';
    END IF;
    IF NEW.upstream_host <> expected_host THEN
        RAISE EXCEPTION 'application route upstream host does not match release identity';
    END IF;
    IF NEW.upstream_port <> expected_port THEN
        RAISE EXCEPTION 'application route upstream port does not match release snapshot';
    END IF;
    IF (NEW.upstream_container <> 'cm-' || NEW.user_app_id::text || '-' || NEW.release_id::text)
       AND (NEW.upstream_container !~ ('^cm-[0-9a-f]{10}-' || NEW.user_app_id::text || '-' || NEW.release_id::text || '$')) THEN
        RAISE EXCEPTION 'application route container does not match application and release identity';
    END IF;
    RETURN NEW;
END $$;

CREATE TRIGGER users_public_identity_immutable
BEFORE UPDATE ON users
FOR EACH ROW EXECUTE FUNCTION protect_user_public_identity();

CREATE TRIGGER user_apps_identity_immutable
BEFORE INSERT OR UPDATE ON user_apps
FOR EACH ROW EXECUTE FUNCTION protect_user_app_identity();

CREATE TRIGGER app_routes_integrity_guard
BEFORE INSERT OR UPDATE ON app_routes
FOR EACH ROW EXECUTE FUNCTION enforce_app_route_integrity();

-- Validate every existing successful Release pointer, including stopped
-- applications that do not currently have a public route.
UPDATE user_apps
SET last_successful_release_id = last_successful_release_id
WHERE last_successful_release_id IS NOT NULL;

-- Validate existing active routes during upgrade instead of preserving a route
-- that cannot be derived from its application and immutable Release.
UPDATE app_routes SET updated_at = updated_at;

COMMIT;
