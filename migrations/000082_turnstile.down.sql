BEGIN;
ALTER TABLE system_state DROP COLUMN IF EXISTS turnstile_registration_protection;
ALTER TABLE system_state DROP COLUMN IF EXISTS turnstile_login_protection;
ALTER TABLE system_state DROP COLUMN IF EXISTS turnstile_secret_key;
ALTER TABLE system_state DROP COLUMN IF EXISTS turnstile_site_key;
ALTER TABLE system_state DROP COLUMN IF EXISTS turnstile_enabled;
COMMIT;
