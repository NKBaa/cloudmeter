BEGIN;
CREATE TABLE app_backups (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_app_id uuid NOT NULL REFERENCES user_apps(id) ON DELETE CASCADE,
    volume_key text NOT NULL,
    docker_volume text NOT NULL,
    storage_key text NOT NULL UNIQUE,
    status text NOT NULL DEFAULT 'queued' CHECK (status IN ('queued','running','succeeded','failed')),
    size_bytes bigint CHECK (size_bytes IS NULL OR size_bytes >= 0),
    last_error text,
    created_at timestamptz NOT NULL DEFAULT now(),
    completed_at timestamptz
);
CREATE TABLE app_restore_jobs (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    backup_id uuid NOT NULL REFERENCES app_backups(id),
    user_app_id uuid NOT NULL REFERENCES user_apps(id) ON DELETE CASCADE,
    status text NOT NULL DEFAULT 'queued' CHECK (status IN ('queued','running','succeeded','failed')),
    last_error text,
    created_at timestamptz NOT NULL DEFAULT now(),
    completed_at timestamptz
);
CREATE INDEX app_backups_app_created_idx ON app_backups(user_app_id,created_at DESC);
CREATE INDEX app_restore_jobs_status_idx ON app_restore_jobs(status,created_at);
COMMIT;
