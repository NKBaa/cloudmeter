BEGIN;

-- Restore the pre-000091 stop-job guard (legacy user_app id based container
-- validation only). This is only meaningful for downgrade rollbacks.
CREATE OR REPLACE FUNCTION enforce_app_stop_job_integrity() RETURNS trigger LANGUAGE plpgsql AS $$
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

COMMIT;
