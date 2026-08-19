BEGIN;

-- The worker is the only component with Docker access. It publishes the
-- latest point-in-time resource sample here for the API and console to read.
CREATE TABLE app_runtime_metrics (
    user_app_id uuid PRIMARY KEY REFERENCES user_apps(id) ON DELETE CASCADE,
    cpu_usage_cores numeric(12,6) NOT NULL DEFAULT 0 CHECK (cpu_usage_cores >= 0),
    memory_usage_bytes bigint NOT NULL DEFAULT 0 CHECK (memory_usage_bytes >= 0),
    sampled_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX app_runtime_metrics_sampled_idx ON app_runtime_metrics(sampled_at DESC);

COMMIT;
