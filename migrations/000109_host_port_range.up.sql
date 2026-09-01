BEGIN;
ALTER TABLE system_settings ADD COLUMN host_port_min integer NOT NULL DEFAULT 30000 CHECK (host_port_min BETWEEN 1 AND 65535);
ALTER TABLE system_settings ADD COLUMN host_port_max integer NOT NULL DEFAULT 40000 CHECK (host_port_max BETWEEN 1 AND 65535);
ALTER TABLE system_settings ADD CONSTRAINT system_settings_host_port_range_check CHECK (host_port_min <= host_port_max);
COMMIT;
