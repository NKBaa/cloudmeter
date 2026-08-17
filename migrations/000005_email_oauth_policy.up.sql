BEGIN;
ALTER TABLE system_state ADD COLUMN email_domain_whitelist text[] NOT NULL DEFAULT ARRAY[]::text[];
ALTER TABLE system_state ADD COLUMN block_email_aliases boolean NOT NULL DEFAULT true;
CREATE TABLE oauth_settings (
    provider text PRIMARY KEY CHECK (provider IN ('github','linuxdo')),
    enabled boolean NOT NULL DEFAULT false,
    client_id text NOT NULL DEFAULT '',
    client_secret text NOT NULL DEFAULT '',
    scopes text NOT NULL DEFAULT 'openid email profile',
    updated_at timestamptz NOT NULL DEFAULT now()
);
INSERT INTO oauth_settings(provider,scopes) VALUES ('github','read:user user:email'),('linuxdo','openid email profile');
COMMIT;
