BEGIN;
ALTER TABLE system_state ADD COLUMN IF NOT EXISTS password_login_enabled boolean NOT NULL DEFAULT true;
ALTER TABLE system_state ADD COLUMN IF NOT EXISTS password_registration_enabled boolean NOT NULL DEFAULT true;
COMMIT;
