BEGIN;

CREATE TABLE homepage_settings (
    singleton boolean PRIMARY KEY DEFAULT true CHECK (singleton),
    content_html text NOT NULL DEFAULT '',
    updated_at timestamptz NOT NULL DEFAULT now(),
    updated_by uuid REFERENCES users(id)
);

INSERT INTO homepage_settings(singleton, content_html)
SELECT true, homepage_content FROM system_settings WHERE singleton
ON CONFLICT DO NOTHING;

COMMIT;
