BEGIN;

-- Optional per-instance host port published by the Worker when the product
-- version enables direct port mapping. NULL means the route is only reachable
-- through the platform gateway.
ALTER TABLE app_routes ADD COLUMN IF NOT EXISTS host_port integer CHECK (host_port IS NULL OR host_port BETWEEN 1 AND 65535);

-- The resolved host port is staged on the release row (created by the Worker)
-- so the route upsert in the deployment-succeeded state can read it.
ALTER TABLE app_releases ADD COLUMN IF NOT EXISTS host_port integer CHECK (host_port IS NULL OR host_port BETWEEN 1 AND 65535);

COMMIT;
