BEGIN;
CREATE TABLE deployment_events (
    id bigserial PRIMARY KEY,
    deployment_job_id uuid NOT NULL REFERENCES deployment_jobs(id) ON DELETE CASCADE,
    from_state text,
    to_state text NOT NULL,
    message text,
    metadata jsonb NOT NULL DEFAULT '{}',
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX deployment_events_job_created_idx ON deployment_events(deployment_job_id, created_at);
COMMIT;

