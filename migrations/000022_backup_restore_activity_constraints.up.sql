BEGIN;
CREATE UNIQUE INDEX app_backups_active_volume_uidx
    ON app_backups(user_app_id, volume_key)
    WHERE status IN ('queued','running');
CREATE UNIQUE INDEX app_restore_jobs_active_app_uidx
    ON app_restore_jobs(user_app_id)
    WHERE status IN ('queued','running');
COMMIT;
