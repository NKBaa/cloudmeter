BEGIN;
ALTER TABLE app_routes ADD COLUMN upstream_container text NOT NULL DEFAULT '';
UPDATE app_routes SET upstream_container=upstream_host WHERE upstream_host LIKE 'cm-%';
COMMIT;
