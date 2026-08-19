BEGIN;

CREATE TABLE platform_restart_requests (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    requested_by uuid NOT NULL REFERENCES users(id),
    status text NOT NULL DEFAULT 'queued' CHECK (status IN ('queued','running','succeeded','failed')),
    attempts integer NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    last_error text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now(),
    started_at timestamptz,
    completed_at timestamptz,
    updated_at timestamptz NOT NULL DEFAULT now()
);

-- A restart briefly interrupts the control plane. Serializing requests keeps
-- repeated clicks from turning into an endless restart loop.
CREATE UNIQUE INDEX platform_restart_requests_one_active_idx
    ON platform_restart_requests ((true))
    WHERE status IN ('queued','running');
CREATE INDEX platform_restart_requests_created_idx
    ON platform_restart_requests (created_at DESC);

COMMIT;
