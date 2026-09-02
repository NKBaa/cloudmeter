BEGIN;
DROP TABLE IF EXISTS app_port_mappings;
CREATE OR REPLACE FUNCTION normalize_selected_release_route(spec jsonb, template jsonb) RETURNS jsonb
LANGUAGE sql IMMUTABLE AS $$ SELECT spec $$;
COMMIT;
