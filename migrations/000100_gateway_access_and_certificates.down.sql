BEGIN;

DROP TABLE IF EXISTS gateway_certificates;

ALTER TABLE system_settings
    DROP COLUMN IF EXISTS acme_ca,
    DROP COLUMN IF EXISTS acme_email,
    DROP COLUMN IF EXISTS app_certificate_mode,
    DROP COLUMN IF EXISTS console_certificate_mode,
    DROP COLUMN IF EXISTS http3_enabled,
    DROP COLUMN IF EXISTS hsts_enabled,
    DROP COLUMN IF EXISTS http_policy,
    DROP COLUMN IF EXISTS tls_enabled,
    DROP COLUMN IF EXISTS access_mode;

COMMIT;
