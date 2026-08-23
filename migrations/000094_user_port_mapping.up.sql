BEGIN;

-- Per-instance opt-in to direct host port publishing. The product template
-- declares the capability (route_spec.portMapping.available); the user decides
-- per instance whether to enable it, and the Worker auto-assigns a free host
-- port on every deployment.
ALTER TABLE user_apps ADD COLUMN IF NOT EXISTS port_mapping_enabled boolean NOT NULL DEFAULT false;

COMMIT;
