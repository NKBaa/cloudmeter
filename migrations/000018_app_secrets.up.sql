BEGIN;
CREATE TABLE app_secrets (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_app_id uuid NOT NULL REFERENCES user_apps(id) ON DELETE CASCADE,
    key text NOT NULL CHECK (key ~ '^[A-Z_][A-Z0-9_]{0,127}$'),
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (user_app_id, key)
);

CREATE TABLE app_secret_versions (
    id uuid PRIMARY KEY,
    app_secret_id uuid NOT NULL REFERENCES app_secrets(id) ON DELETE CASCADE,
    version integer NOT NULL CHECK (version > 0),
    encrypted_value text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (app_secret_id, version)
);
CREATE INDEX app_secret_versions_latest_idx ON app_secret_versions(app_secret_id, version DESC);
COMMIT;
