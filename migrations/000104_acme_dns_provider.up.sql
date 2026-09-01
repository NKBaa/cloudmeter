BEGIN;

ALTER TABLE system_settings
    ADD COLUMN acme_dns_provider text NOT NULL DEFAULT ''
        CHECK (acme_dns_provider IN ('', 'cloudflare', 'alidns', 'tencentcloud', 'route53', 'digitalocean')),
    ADD COLUMN acme_dns_credentials text NOT NULL DEFAULT '';

COMMIT;
