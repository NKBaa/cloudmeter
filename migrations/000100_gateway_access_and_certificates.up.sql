BEGIN;

ALTER TABLE system_settings
    ADD COLUMN access_mode text NOT NULL DEFAULT 'apps_only'
        CHECK (access_mode IN ('all_caddy', 'apps_only')),
    ADD COLUMN tls_enabled boolean NOT NULL DEFAULT false,
    ADD COLUMN http_policy text NOT NULL DEFAULT 'redirect'
        CHECK (http_policy IN ('redirect', 'allow', 'https_only')),
    ADD COLUMN hsts_enabled boolean NOT NULL DEFAULT false,
    ADD COLUMN http3_enabled boolean NOT NULL DEFAULT true,
    ADD COLUMN console_certificate_mode text NOT NULL DEFAULT 'automatic'
        CHECK (console_certificate_mode IN ('automatic', 'imported')),
    ADD COLUMN app_certificate_mode text NOT NULL DEFAULT 'automatic'
        CHECK (app_certificate_mode IN ('automatic', 'imported')),
    ADD COLUMN acme_email text NOT NULL DEFAULT '',
    ADD COLUMN acme_ca text NOT NULL DEFAULT 'https://acme-v02.api.letsencrypt.org/directory';

CREATE TABLE gateway_certificates (
    target text PRIMARY KEY CHECK (target IN ('console', 'applications')),
    certificate_pem text NOT NULL,
    private_key_pem text NOT NULL,
    common_name text NOT NULL DEFAULT '',
    dns_names jsonb NOT NULL DEFAULT '[]'::jsonb CHECK (jsonb_typeof(dns_names)='array'),
    issuer text NOT NULL DEFAULT '',
    not_before timestamptz NOT NULL,
    not_after timestamptz NOT NULL,
    fingerprint_sha256 text NOT NULL,
    updated_at timestamptz NOT NULL DEFAULT now(),
    updated_by uuid REFERENCES users(id) ON DELETE SET NULL
);

COMMIT;
