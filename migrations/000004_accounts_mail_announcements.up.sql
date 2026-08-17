BEGIN;
ALTER TABLE system_state ADD COLUMN email_verification_required boolean NOT NULL DEFAULT false;
ALTER TABLE users ADD COLUMN email_verified_at timestamptz;

CREATE TABLE smtp_settings (
    singleton boolean PRIMARY KEY DEFAULT true CHECK (singleton),
    host text NOT NULL DEFAULT '',
    port integer NOT NULL DEFAULT 587 CHECK (port BETWEEN 1 AND 65535),
    username text NOT NULL DEFAULT '',
    password text NOT NULL DEFAULT '',
    from_email text NOT NULL DEFAULT '',
    from_name text NOT NULL DEFAULT 'CloudMeter',
    tls_mode text NOT NULL DEFAULT 'starttls' CHECK (tls_mode IN ('starttls','tls','none')),
    enabled boolean NOT NULL DEFAULT false,
    updated_at timestamptz NOT NULL DEFAULT now()
);
INSERT INTO smtp_settings(singleton) VALUES(true);

CREATE TABLE email_verification_codes (
    id bigserial PRIMARY KEY,
    email text NOT NULL,
    purpose text NOT NULL DEFAULT 'register' CHECK (purpose IN ('register')),
    code_hash bytea NOT NULL,
    expires_at timestamptz NOT NULL,
    consumed_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX email_verification_codes_lookup_idx ON email_verification_codes(lower(email), purpose, created_at DESC);

CREATE TABLE announcements (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    title text NOT NULL,
    content text NOT NULL,
    severity text NOT NULL DEFAULT 'info' CHECK (severity IN ('info','warning','critical')),
    published boolean NOT NULL DEFAULT false,
    starts_at timestamptz NOT NULL DEFAULT now(),
    ends_at timestamptz,
    created_by uuid NOT NULL REFERENCES users(id),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CHECK (ends_at IS NULL OR ends_at > starts_at)
);
CREATE INDEX announcements_active_idx ON announcements(published, starts_at DESC);
COMMIT;
