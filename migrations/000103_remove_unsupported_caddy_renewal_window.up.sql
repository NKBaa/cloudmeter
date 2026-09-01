BEGIN;

ALTER TABLE system_settings
    DROP COLUMN IF EXISTS certificate_renewal_window_ratio;

COMMIT;
