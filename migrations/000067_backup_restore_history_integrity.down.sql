BEGIN;
DROP TRIGGER IF EXISTS app_restore_jobs_no_truncate ON app_restore_jobs;
DROP TRIGGER IF EXISTS app_restore_jobs_no_delete ON app_restore_jobs;
DROP TRIGGER IF EXISTS app_backups_no_truncate ON app_backups;
DROP TRIGGER IF EXISTS app_backups_no_delete ON app_backups;
DROP TRIGGER IF EXISTS app_restore_jobs_integrity_guard ON app_restore_jobs;
DROP TRIGGER IF EXISTS app_backups_integrity_guard ON app_backups;
DROP FUNCTION IF EXISTS enforce_app_restore_job_integrity;
DROP FUNCTION IF EXISTS enforce_app_backup_integrity;
ALTER TABLE app_restore_jobs
    DROP CONSTRAINT IF EXISTS app_restore_jobs_result_check,
    DROP CONSTRAINT IF EXISTS app_restore_jobs_completion_check,
    DROP CONSTRAINT IF EXISTS app_restore_jobs_idempotency_key_length_check;
ALTER TABLE app_backups
    DROP CONSTRAINT IF EXISTS app_backups_result_check,
    DROP CONSTRAINT IF EXISTS app_backups_completion_check,
    DROP CONSTRAINT IF EXISTS app_backups_volume_key_format_check;
COMMIT;
