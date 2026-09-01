BEGIN;

ALTER TABLE system_settings
    ADD COLUMN acme_key_type text NOT NULL DEFAULT 'p256'
        CHECK (acme_key_type IN ('ed25519', 'p256', 'p384', 'rsa2048', 'rsa4096')),
    ADD COLUMN certificate_renew_interval_minutes integer NOT NULL DEFAULT 10
        CHECK (certificate_renew_interval_minutes BETWEEN 1 AND 1440);

COMMIT;
