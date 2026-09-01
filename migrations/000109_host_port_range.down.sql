BEGIN;
ALTER TABLE system_settings DROP CONSTRAINT IF EXISTS system_settings_host_port_range_check;
ALTER TABLE system_settings DROP COLUMN IF EXISTS host_port_min;
ALTER TABLE system_settings DROP COLUMN IF EXISTS host_port_max;
COMMIT;
