BEGIN;

ALTER TABLE system_settings
    ADD COLUMN IF NOT EXISTS certificate_renewal_window_ratio numeric(4,3) NOT NULL DEFAULT 0.333
        CHECK (certificate_renewal_window_ratio BETWEEN 0.100 AND 0.900);

COMMIT;
