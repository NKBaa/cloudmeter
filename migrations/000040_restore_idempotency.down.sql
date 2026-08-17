BEGIN;
DROP INDEX IF EXISTS app_restore_jobs_idempotency_uidx;
ALTER TABLE app_restore_jobs DROP COLUMN IF EXISTS idempotency_key;
COMMIT;
