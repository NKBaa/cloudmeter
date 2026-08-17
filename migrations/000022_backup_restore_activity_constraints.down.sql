BEGIN;
DROP INDEX IF EXISTS app_restore_jobs_active_app_uidx;
DROP INDEX IF EXISTS app_backups_active_volume_uidx;
COMMIT;
