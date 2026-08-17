BEGIN;

CREATE TABLE app_product_version_tests (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    product_version_id uuid NOT NULL REFERENCES app_product_versions(id) ON DELETE CASCADE,
    requested_by uuid NOT NULL REFERENCES users(id),
    state text NOT NULL DEFAULT 'queued' CHECK (state IN ('queued','pulling','starting','health_checking','succeeded','failed')),
    immutable_snapshot jsonb NOT NULL,
    encrypted_secrets jsonb NOT NULL DEFAULT '{}'::jsonb,
    attempts integer NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    health_attempts integer NOT NULL DEFAULT 0 CHECK (health_attempts >= 0),
    available_at timestamptz NOT NULL DEFAULT now(),
    last_error text,
    started_at timestamptz,
    completed_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CHECK (jsonb_typeof(immutable_snapshot) = 'object'),
    CHECK (jsonb_typeof(encrypted_secrets) = 'object'),
    CHECK ((state IN ('succeeded','failed')) = (completed_at IS NOT NULL))
);

CREATE UNIQUE INDEX app_product_version_tests_active_unique
    ON app_product_version_tests(product_version_id)
    WHERE state NOT IN ('succeeded','failed');
CREATE INDEX app_product_version_tests_worker_idx
    ON app_product_version_tests(state,available_at,created_at)
    WHERE state NOT IN ('succeeded','failed');
CREATE INDEX app_product_version_tests_version_idx
    ON app_product_version_tests(product_version_id,created_at DESC);

CREATE FUNCTION protect_product_version_test_snapshot() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF OLD.state IN ('succeeded','failed') THEN
        RAISE EXCEPTION 'completed product version tests are immutable';
    END IF;
    IF NEW.product_version_id <> OLD.product_version_id
       OR NEW.requested_by <> OLD.requested_by
       OR NEW.immutable_snapshot <> OLD.immutable_snapshot
       OR NEW.created_at <> OLD.created_at THEN
        RAISE EXCEPTION 'product version test snapshot is immutable';
    END IF;
    IF NOT (
        (OLD.state = 'queued' AND NEW.state IN ('pulling','failed'))
        OR (OLD.state = 'pulling' AND NEW.state IN ('starting','failed'))
        OR (OLD.state = 'starting' AND NEW.state IN ('health_checking','failed'))
        OR (OLD.state = 'health_checking' AND NEW.state IN ('health_checking','succeeded','failed'))
    ) THEN
        RAISE EXCEPTION 'invalid product version test transition: % -> %', OLD.state, NEW.state;
    END IF;
    IF NEW.encrypted_secrets <> OLD.encrypted_secrets
       AND (NEW.state NOT IN ('succeeded','failed') OR NEW.encrypted_secrets <> '{}'::jsonb) THEN
        RAISE EXCEPTION 'product version test secrets may only be cleared on completion';
    END IF;
    RETURN NEW;
END $$;
CREATE TRIGGER app_product_version_tests_snapshot_immutable
    BEFORE UPDATE ON app_product_version_tests
    FOR EACH ROW EXECUTE FUNCTION protect_product_version_test_snapshot();

CREATE FUNCTION protect_product_version_configuration() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.product_id <> OLD.product_id
       OR NEW.version <> OLD.version
       OR NEW.image_digest <> OLD.image_digest
       OR NEW.runtime_spec <> OLD.runtime_spec
       OR NEW.route_spec <> OLD.route_spec
       OR NEW.health_spec <> OLD.health_spec
       OR NEW.update_spec <> OLD.update_spec
       OR NEW.created_at <> OLD.created_at THEN
        RAISE EXCEPTION 'product version configuration is immutable';
    END IF;
    IF OLD.published_at IS NOT NULL AND NEW.published_at IS DISTINCT FROM OLD.published_at THEN
        RAISE EXCEPTION 'published product version cannot be unpublished';
    END IF;
    IF OLD.published_at IS NULL AND NEW.published_at IS NOT NULL
       AND NOT EXISTS (SELECT 1 FROM app_product_version_tests WHERE product_version_id=OLD.id AND state='succeeded') THEN
        RAISE EXCEPTION 'a successful product version test is required before publishing';
    END IF;
    RETURN NEW;
END $$;
CREATE TRIGGER app_product_versions_configuration_immutable
    BEFORE UPDATE ON app_product_versions
    FOR EACH ROW EXECUTE FUNCTION protect_product_version_configuration();

COMMIT;
