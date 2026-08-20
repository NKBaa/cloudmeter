BEGIN;
CREATE TABLE docker_image_inventory (
    image_id text PRIMARY KEY,
    repo_tags text[] NOT NULL DEFAULT '{}',
    size_bytes bigint NOT NULL CHECK(size_bytes>=0),
    created_at timestamptz,
    container_references integer NOT NULL DEFAULT 0 CHECK(container_references>=0),
    sampled_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE docker_image_deletion_jobs (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    image_id text NOT NULL,
    requested_by uuid NOT NULL REFERENCES users(id),
    status text NOT NULL DEFAULT 'queued' CHECK(status IN ('queued','running','succeeded','failed')),
    last_error text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    completed_at timestamptz
);
CREATE UNIQUE INDEX docker_image_deletion_active_idx ON docker_image_deletion_jobs(image_id) WHERE status IN ('queued','running');
COMMIT;
