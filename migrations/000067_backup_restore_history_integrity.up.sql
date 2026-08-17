BEGIN;

ALTER TABLE app_backups
    ADD CONSTRAINT app_backups_volume_key_format_check
    CHECK (volume_key ~ '^[a-z][a-z0-9-]{0,31}$'),
    ADD CONSTRAINT app_backups_completion_check
    CHECK ((status IN ('queued','running') AND completed_at IS NULL)
        OR (status IN ('succeeded','failed') AND completed_at IS NOT NULL)),
    ADD CONSTRAINT app_backups_result_check
    CHECK ((status = 'succeeded' AND reserved_bytes = 0 AND last_error IS NULL)
        OR (status = 'failed' AND reserved_bytes = 0 AND last_error IS NOT NULL)
        OR status IN ('queued','running'));

ALTER TABLE app_restore_jobs
    ADD CONSTRAINT app_restore_jobs_idempotency_key_length_check
    CHECK (length(idempotency_key) BETWEEN 1 AND 128),
    ADD CONSTRAINT app_restore_jobs_completion_check
    CHECK ((status IN ('queued','running') AND completed_at IS NULL)
        OR (status IN ('succeeded','failed') AND completed_at IS NOT NULL)),
    ADD CONSTRAINT app_restore_jobs_result_check
    CHECK ((status = 'succeeded' AND last_error IS NULL)
        OR (status = 'failed' AND last_error IS NOT NULL)
        OR status IN ('queued','running'));

CREATE FUNCTION enforce_app_backup_integrity() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP = 'UPDATE'
       AND (NEW.id <> OLD.id
            OR NEW.user_app_id <> OLD.user_app_id
            OR NEW.volume_key <> OLD.volume_key
            OR NEW.docker_volume <> OLD.docker_volume
            OR NEW.storage_key <> OLD.storage_key
            OR NEW.created_at <> OLD.created_at) THEN
        RAISE EXCEPTION 'application backup identity is immutable';
    END IF;
    IF TG_OP = 'UPDATE' AND OLD.status IN ('succeeded','failed')
       AND NEW IS DISTINCT FROM OLD THEN
        RAISE EXCEPTION 'completed application backups are immutable';
    END IF;
    IF TG_OP = 'INSERT'
       AND (NEW.status <> 'queued'
            OR NEW.size_bytes IS NOT NULL
            OR NEW.last_error IS NOT NULL
            OR NEW.completed_at IS NOT NULL) THEN
        RAISE EXCEPTION 'application backups must be inserted in a clean queued state';
    END IF;
    IF TG_OP = 'UPDATE' AND NEW.status <> OLD.status AND NOT (
        (OLD.status = 'queued' AND NEW.status = 'running')
        OR (OLD.status = 'running' AND NEW.status IN ('succeeded','failed'))
    ) THEN
        RAISE EXCEPTION 'invalid application backup transition: % -> %', OLD.status, NEW.status;
    END IF;
    IF TG_OP = 'UPDATE' AND NEW.status <> OLD.status
       AND NEW.status = 'succeeded' AND NEW.size_bytes IS NULL THEN
        RAISE EXCEPTION 'successful application backups must record their observed size';
    END IF;
    RETURN NEW;
END $$;

CREATE FUNCTION enforce_app_restore_job_integrity() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP = 'UPDATE'
       AND (NEW.id <> OLD.id
            OR NEW.backup_id <> OLD.backup_id
            OR NEW.user_app_id <> OLD.user_app_id
            OR NEW.idempotency_key <> OLD.idempotency_key
            OR NEW.created_at <> OLD.created_at) THEN
        RAISE EXCEPTION 'application restore job identity is immutable';
    END IF;
    IF TG_OP = 'UPDATE' AND OLD.status IN ('succeeded','failed')
       AND NEW IS DISTINCT FROM OLD THEN
        RAISE EXCEPTION 'completed application restore jobs are immutable';
    END IF;
    IF NOT EXISTS (
        SELECT 1
        FROM app_backups backup
        WHERE backup.id = NEW.backup_id
          AND backup.user_app_id = NEW.user_app_id
          AND backup.status = 'succeeded'
    ) THEN
        RAISE EXCEPTION 'application restore job must reference a successful backup of the same application';
    END IF;
    IF TG_OP = 'INSERT'
       AND (NEW.status <> 'queued'
            OR NEW.last_error IS NOT NULL
            OR NEW.completed_at IS NOT NULL) THEN
        RAISE EXCEPTION 'application restore jobs must be inserted in a clean queued state';
    END IF;
    IF TG_OP = 'UPDATE' AND NEW.status <> OLD.status AND NOT (
        (OLD.status = 'queued' AND NEW.status IN ('running','failed'))
        OR (OLD.status = 'running' AND NEW.status IN ('succeeded','failed'))
    ) THEN
        RAISE EXCEPTION 'invalid application restore transition: % -> %', OLD.status, NEW.status;
    END IF;
    RETURN NEW;
END $$;

CREATE TRIGGER app_backups_integrity_guard
BEFORE INSERT OR UPDATE ON app_backups
FOR EACH ROW EXECUTE FUNCTION enforce_app_backup_integrity();

CREATE TRIGGER app_restore_jobs_integrity_guard
BEFORE INSERT OR UPDATE ON app_restore_jobs
FOR EACH ROW EXECUTE FUNCTION enforce_app_restore_job_integrity();

CREATE TRIGGER app_backups_no_delete
BEFORE DELETE ON app_backups
FOR EACH ROW EXECUTE FUNCTION deny_immutable_history_delete();

CREATE TRIGGER app_backups_no_truncate
BEFORE TRUNCATE ON app_backups
FOR EACH STATEMENT EXECUTE FUNCTION deny_immutable_history_truncate();

CREATE TRIGGER app_restore_jobs_no_delete
BEFORE DELETE ON app_restore_jobs
FOR EACH ROW EXECUTE FUNCTION deny_immutable_history_delete();

CREATE TRIGGER app_restore_jobs_no_truncate
BEFORE TRUNCATE ON app_restore_jobs
FOR EACH STATEMENT EXECUTE FUNCTION deny_immutable_history_truncate();

-- Reject an upgrade containing cross-application restore references, invalid
-- terminal metadata, or a transition history that no longer meets the guards.
-- A successful backup created before size accounting may retain a NULL size;
-- every new running -> succeeded transition must record the observed bytes.
UPDATE app_backups SET status = status;
UPDATE app_restore_jobs SET status = status;

COMMIT;
