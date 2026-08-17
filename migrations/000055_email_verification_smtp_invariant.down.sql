BEGIN;
DROP TRIGGER IF EXISTS smtp_settings_email_verification_guard ON smtp_settings;
DROP TRIGGER IF EXISTS system_state_email_verification_smtp_guard ON system_state;
DROP TRIGGER IF EXISTS smtp_settings_email_verification_settings_lock ON smtp_settings;
DROP TRIGGER IF EXISTS system_state_email_verification_settings_lock ON system_state;
DROP FUNCTION IF EXISTS enforce_email_verification_smtp_invariant();
DROP FUNCTION IF EXISTS lock_email_verification_settings();
COMMIT;
