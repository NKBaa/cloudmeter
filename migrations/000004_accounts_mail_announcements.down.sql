BEGIN;
DROP TABLE IF EXISTS announcements;
DROP TABLE IF EXISTS email_verification_codes;
DROP TABLE IF EXISTS smtp_settings;
ALTER TABLE users DROP COLUMN IF EXISTS email_verified_at;
ALTER TABLE system_state DROP COLUMN IF EXISTS email_verification_required;
COMMIT;
