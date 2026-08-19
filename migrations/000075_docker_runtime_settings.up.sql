BEGIN;

CREATE TABLE docker_runtime_settings (
    singleton boolean PRIMARY KEY DEFAULT true CHECK (singleton),
    registry_mirrors text[] NOT NULL DEFAULT '{}',
    default_registry text NOT NULL DEFAULT '',
    registry_username text NOT NULL DEFAULT '',
    registry_password text NOT NULL DEFAULT '',
    http_proxy text NOT NULL DEFAULT '',
    https_proxy text NOT NULL DEFAULT '',
    no_proxy text NOT NULL DEFAULT 'localhost,127.0.0.1,::1',
    pull_timeout_seconds integer NOT NULL DEFAULT 300 CHECK (pull_timeout_seconds BETWEEN 30 AND 1800),
    detected_registry_mirrors text[] NOT NULL DEFAULT '{}',
    detected_http_proxy text NOT NULL DEFAULT '',
    detected_https_proxy text NOT NULL DEFAULT '',
    detected_no_proxy text NOT NULL DEFAULT '',
    last_checked_at timestamptz,
    last_check_error text NOT NULL DEFAULT '',
    updated_at timestamptz NOT NULL DEFAULT now(),
    updated_by uuid REFERENCES users(id)
);
INSERT INTO docker_runtime_settings(singleton) VALUES (true);

COMMIT;
