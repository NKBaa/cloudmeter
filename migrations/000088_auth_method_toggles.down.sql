BEGIN;
ALTER TABLE system_state DROP COLUMN IF EXISTS password_registration_enabled;
ALTER TABLE system_state DROP COLUMN IF EXISTS password_login_enabled;
COMMIT;
