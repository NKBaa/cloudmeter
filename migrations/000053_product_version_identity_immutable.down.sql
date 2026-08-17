BEGIN;

CREATE OR REPLACE FUNCTION protect_product_version_test_snapshot() RETURNS trigger LANGUAGE plpgsql AS $$
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

CREATE OR REPLACE FUNCTION protect_product_version_configuration() RETURNS trigger LANGUAGE plpgsql AS $$
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

COMMIT;
