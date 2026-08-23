BEGIN;
ALTER TABLE user_apps DROP COLUMN IF EXISTS port_mapping_enabled;
COMMIT;
