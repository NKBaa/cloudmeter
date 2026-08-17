BEGIN;

ALTER TABLE user_apps
    DROP CONSTRAINT user_apps_status_check;

ALTER TABLE user_apps
    ADD CONSTRAINT user_apps_status_check
    CHECK (status IN ('stopped','stopping','deploying','running','updating','suspended','failed'));

ALTER TABLE deployment_jobs
    ADD COLUMN operation text NOT NULL DEFAULT 'deploy'
    CHECK (operation IN ('deploy','update','rollback','start','billing_recovery','subscription_recovery')),
    ADD COLUMN source_release_id uuid REFERENCES app_releases(id);

UPDATE deployment_jobs
SET operation = CASE
    WHEN idempotency_key LIKE 'billing-resume/%' THEN 'billing_recovery'
    WHEN idempotency_key LIKE 'subscription-resume/%'
      OR idempotency_key LIKE 'subscription-purchase/%' THEN 'subscription_recovery'
    WHEN EXISTS (
        SELECT 1 FROM app_releases release
        WHERE release.id = deployment_jobs.release_id AND release.release_number > 1
    ) THEN 'update'
    ELSE 'deploy'
END;

UPDATE deployment_jobs
SET source_release_id = release_id
WHERE operation IN ('billing_recovery','subscription_recovery');

CREATE TABLE app_stop_jobs (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_app_id uuid NOT NULL REFERENCES user_apps(id) ON DELETE CASCADE,
    release_id uuid REFERENCES app_releases(id),
    container_name text NOT NULL DEFAULT '',
    idempotency_key text NOT NULL CHECK (length(idempotency_key) BETWEEN 1 AND 128),
    status text NOT NULL DEFAULT 'queued' CHECK (status IN ('queued','running','succeeded')),
    attempts integer NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    available_at timestamptz NOT NULL DEFAULT now(),
    last_error text,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    completed_at timestamptz,
    UNIQUE (user_app_id, idempotency_key),
    CHECK ((status = 'succeeded') = (completed_at IS NOT NULL))
);

CREATE UNIQUE INDEX app_stop_jobs_active_app_uidx
    ON app_stop_jobs(user_app_id)
    WHERE status IN ('queued','running');

CREATE INDEX app_stop_jobs_queue_idx
    ON app_stop_jobs(status, available_at, created_at);

COMMIT;
