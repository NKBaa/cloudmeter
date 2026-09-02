BEGIN;
ALTER TABLE system_settings ADD COLUMN port_mapping_host text NOT NULL DEFAULT '';
COMMIT;
