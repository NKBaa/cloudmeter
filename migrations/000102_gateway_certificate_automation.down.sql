BEGIN;

ALTER TABLE system_settings
    DROP COLUMN IF EXISTS certificate_renew_interval_minutes,
    DROP COLUMN IF EXISTS acme_key_type;

COMMIT;
