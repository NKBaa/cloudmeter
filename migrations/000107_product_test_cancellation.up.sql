BEGIN;

ALTER TABLE app_product_version_tests DROP CONSTRAINT app_product_version_tests_state_check;
ALTER TABLE app_product_version_tests DROP CONSTRAINT app_product_version_tests_check;
ALTER TABLE app_product_version_tests
    ADD CONSTRAINT app_product_version_tests_state_check
    CHECK (state IN ('queued','pulling','starting','health_checking','succeeded','failed','cancelled')),
    ADD CONSTRAINT app_product_version_tests_check
    CHECK ((state IN ('succeeded','failed','cancelled')) = (completed_at IS NOT NULL));

DROP INDEX app_product_version_tests_active_unique;
DROP INDEX app_product_version_tests_worker_idx;
CREATE UNIQUE INDEX app_product_version_tests_active_unique
    ON app_product_version_tests(product_version_id)
    WHERE state NOT IN ('succeeded','failed','cancelled');
CREATE INDEX app_product_version_tests_worker_idx
    ON app_product_version_tests(state,available_at,created_at)
    WHERE state NOT IN ('succeeded','failed','cancelled');

CREATE TABLE app_product_version_test_cancellations (
    test_id uuid PRIMARY KEY REFERENCES app_product_version_tests(id),
    requested_by uuid NOT NULL REFERENCES users(id),
    requested_at timestamptz NOT NULL DEFAULT now(),
    processed_at timestamptz
);
CREATE INDEX app_product_version_test_cancellations_pending_idx
    ON app_product_version_test_cancellations(requested_at) WHERE processed_at IS NULL;

CREATE OR REPLACE FUNCTION protect_product_version_test_snapshot() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF OLD.state IN ('succeeded','failed','cancelled') THEN
        RAISE EXCEPTION 'completed product version tests are immutable';
    END IF;
    IF NEW.product_version_id <> OLD.product_version_id
       OR NEW.requested_by <> OLD.requested_by
       OR NEW.immutable_snapshot <> OLD.immutable_snapshot
       OR NEW.created_at <> OLD.created_at THEN
        RAISE EXCEPTION 'product version test snapshot is immutable';
    END IF;
    IF NOT (
        (OLD.state = 'queued' AND NEW.state IN ('pulling','failed','cancelled'))
        OR (OLD.state = 'pulling' AND NEW.state IN ('starting','failed','cancelled'))
        OR (OLD.state = 'starting' AND NEW.state IN ('health_checking','failed','cancelled'))
        OR (OLD.state = 'health_checking' AND NEW.state IN ('health_checking','succeeded','failed','cancelled'))
    ) THEN
        RAISE EXCEPTION 'invalid product version test transition: % -> %', OLD.state, NEW.state;
    END IF;
    IF NEW.encrypted_secrets <> OLD.encrypted_secrets
       AND (NEW.state NOT IN ('succeeded','failed','cancelled') OR NEW.encrypted_secrets <> '{}'::jsonb) THEN
        RAISE EXCEPTION 'product version test secrets may only be cleared on completion';
    END IF;
    RETURN NEW;
END $$;

COMMIT;
