BEGIN;
ALTER TABLE system_state ADD COLUMN turnstile_enabled boolean NOT NULL DEFAULT false;
ALTER TABLE system_state ADD COLUMN turnstile_site_key text NOT NULL DEFAULT '';
ALTER TABLE system_state ADD COLUMN turnstile_secret_key text NOT NULL DEFAULT '';
ALTER TABLE system_state ADD COLUMN turnstile_login_protection boolean NOT NULL DEFAULT false;
ALTER TABLE system_state ADD COLUMN turnstile_registration_protection boolean NOT NULL DEFAULT false;
COMMIT;
