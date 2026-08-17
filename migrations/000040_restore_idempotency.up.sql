BEGIN;
ALTER TABLE app_restore_jobs ADD COLUMN idempotency_key text;
UPDATE app_restore_jobs SET idempotency_key='legacy-'||id::text WHERE idempotency_key IS NULL;
ALTER TABLE app_restore_jobs ALTER COLUMN idempotency_key SET NOT NULL;
CREATE UNIQUE INDEX app_restore_jobs_idempotency_uidx ON app_restore_jobs(user_app_id,idempotency_key);
COMMIT;
