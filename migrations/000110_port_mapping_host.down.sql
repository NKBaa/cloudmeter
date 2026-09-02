BEGIN;
ALTER TABLE system_settings DROP COLUMN IF EXISTS port_mapping_host;
COMMIT;
