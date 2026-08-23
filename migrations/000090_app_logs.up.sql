BEGIN;
CREATE TABLE IF NOT EXISTS app_log_fetch_jobs (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
 user_app_id uuid NOT NULL REFERENCES user_apps(id) ON DELETE CASCADE,
 requested_by uuid NOT NULL REFERENCES users(id),
 status text NOT NULL DEFAULT 'queued' CHECK (status IN ('queued','running','succeeded','failed')),
 attempts integer NOT NULL DEFAULT 0,
 last_error text NOT NULL DEFAULT '',
 available_at timestamptz NOT NULL DEFAULT now(),
 requested_at timestamptz NOT NULL DEFAULT now(),
 completed_at timestamptz,
 updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS app_log_fetch_jobs_claim_idx ON app_log_fetch_jobs(status, available_at);
CREATE TABLE IF NOT EXISTS app_runtime_logs (
 user_app_id uuid PRIMARY KEY REFERENCES user_apps(id) ON DELETE CASCADE,
 log_text text NOT NULL DEFAULT '',
 sampled_at timestamptz,
 updated_at timestamptz NOT NULL DEFAULT now()
);
COMMIT;
