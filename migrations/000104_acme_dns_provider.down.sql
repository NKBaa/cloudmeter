BEGIN;

ALTER TABLE system_settings
    DROP COLUMN IF EXISTS acme_dns_credentials,
    DROP COLUMN IF EXISTS acme_dns_provider;

COMMIT;
