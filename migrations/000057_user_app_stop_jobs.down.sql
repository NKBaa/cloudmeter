BEGIN;

UPDATE user_apps
SET status = 'stopped', suspension_reason = NULL
WHERE status = 'stopping';

DROP TABLE app_stop_jobs;

ALTER TABLE deployment_jobs
    DROP COLUMN source_release_id,
    DROP COLUMN operation;

ALTER TABLE user_apps
    DROP CONSTRAINT user_apps_status_check;

ALTER TABLE user_apps
    ADD CONSTRAINT user_apps_status_check
    CHECK (status IN ('stopped','deploying','running','updating','suspended','failed'));

COMMIT;
