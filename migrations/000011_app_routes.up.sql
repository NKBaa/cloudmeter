BEGIN;
CREATE TABLE app_routes (
    user_app_id uuid PRIMARY KEY REFERENCES user_apps(id) ON DELETE CASCADE,
    release_id uuid NOT NULL REFERENCES app_releases(id),
    public_path text NOT NULL UNIQUE,
    upstream_host text NOT NULL,
    upstream_port integer NOT NULL CHECK (upstream_port BETWEEN 1 AND 65535),
    updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX app_routes_release_idx ON app_routes(release_id);
COMMIT;
