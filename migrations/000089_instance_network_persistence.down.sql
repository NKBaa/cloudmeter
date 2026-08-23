BEGIN;
ALTER TABLE app_releases DROP COLUMN IF EXISTS container_id;
ALTER TABLE app_routes DROP COLUMN IF EXISTS container_id;
ALTER TABLE app_routes DROP COLUMN IF EXISTS network_name;
COMMIT;
