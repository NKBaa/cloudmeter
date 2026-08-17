BEGIN;

CREATE FUNCTION runtime_dependencies_spec_valid(spec jsonb) RETURNS boolean
LANGUAGE plpgsql IMMUTABLE AS $$
DECLARE
    dependency jsonb;
    dependency_key text;
    service_slug text;
    seen_keys text[] := ARRAY[]::text[];
    seen_services text[] := ARRAY[]::text[];
BEGIN
    IF NOT (spec ? 'dependencies') THEN
        RETURN true;
    END IF;
    IF jsonb_typeof(spec->'dependencies') IS DISTINCT FROM 'array'
       OR jsonb_array_length(spec->'dependencies') > 32 THEN
        RETURN false;
    END IF;
    FOR dependency IN SELECT value FROM jsonb_array_elements(spec->'dependencies') LOOP
        IF jsonb_typeof(dependency) IS DISTINCT FROM 'object'
           OR NOT (dependency ?& ARRAY['key','productId','serviceSlug','required'])
           OR EXISTS (
               SELECT 1 FROM jsonb_object_keys(dependency) AS fields(field)
               WHERE field <> ALL(ARRAY['key','productId','serviceSlug','required'])
           )
           OR jsonb_typeof(dependency->'key') IS DISTINCT FROM 'string'
           OR jsonb_typeof(dependency->'productId') IS DISTINCT FROM 'string'
           OR jsonb_typeof(dependency->'serviceSlug') IS DISTINCT FROM 'string'
           OR jsonb_typeof(dependency->'required') IS DISTINCT FROM 'boolean'
           OR dependency->>'key' !~ '^[a-z][a-z0-9-]{0,31}$'
           OR dependency->>'productId' !~ '^[0-9a-f]{8}-[0-9a-f]{4}-[1-5][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$'
           OR dependency->>'serviceSlug' !~ '^[a-z0-9][a-z0-9-]{0,62}$' THEN
            RETURN false;
        END IF;
        dependency_key := dependency->>'key';
        service_slug := dependency->>'serviceSlug';
        IF dependency_key = ANY(seen_keys) OR service_slug = ANY(seen_services) THEN
            RETURN false;
        END IF;
        seen_keys := array_append(seen_keys, dependency_key);
        seen_services := array_append(seen_services, service_slug);
    END LOOP;
    RETURN true;
END $$;

ALTER TABLE app_product_versions
    ADD CONSTRAINT app_product_versions_runtime_dependencies_check
    CHECK (runtime_dependencies_spec_valid(runtime_spec));

CREATE FUNCTION protect_published_product_dependencies() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
    dependency jsonb;
    target_product_id uuid;
    cyclic boolean;
BEGIN
    IF TG_OP = 'INSERT' THEN
        IF NEW.published_at IS NOT NULL THEN
            RAISE EXCEPTION 'product versions must be inserted unpublished and complete a successful test before publishing';
        END IF;
        RETURN NEW;
    END IF;
    IF OLD.published_at IS NOT NULL OR NEW.published_at IS NULL THEN
        RETURN NEW;
    END IF;

    -- API publishing takes the same lock before reading the graph. A direct
    -- concurrent UPDATE is rejected instead of waiting with a stale statement
    -- snapshot and must be retried after the other publisher commits.
    IF NOT pg_try_advisory_xact_lock(729104503) THEN
        RAISE EXCEPTION 'product dependency graph is being modified; retry publication';
    END IF;
    FOR dependency IN SELECT value FROM jsonb_array_elements(coalesce(NEW.runtime_spec->'dependencies','[]'::jsonb)) LOOP
        target_product_id := (dependency->>'productId')::uuid;
        IF target_product_id = NEW.product_id THEN
            RAISE EXCEPTION 'a product version cannot depend on its own product';
        END IF;
        IF NOT EXISTS (
            SELECT 1 FROM app_products p
            JOIN app_product_versions pv ON pv.product_id=p.id
            WHERE p.id=target_product_id
              AND p.status='published'
              AND pv.published_at IS NOT NULL
        ) THEN
            RAISE EXCEPTION 'product dependency % must target a published product', dependency->>'key';
        END IF;

        WITH RECURSIVE edges AS (
            SELECT pv.product_id AS source_id,(item->>'productId')::uuid AS target_id
            FROM app_product_versions pv
            CROSS JOIN LATERAL jsonb_array_elements(coalesce(pv.runtime_spec->'dependencies','[]'::jsonb)) item
            WHERE pv.published_at IS NOT NULL
              AND item->>'productId' ~ '^[0-9a-f]{8}-[0-9a-f]{4}-[1-5][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$'
        ), reachable(id) AS (
            SELECT target_product_id
            UNION
            SELECT edges.target_id FROM reachable JOIN edges ON edges.source_id=reachable.id
        )
        SELECT EXISTS(SELECT 1 FROM reachable WHERE id=NEW.product_id) INTO cyclic;
        IF cyclic THEN
            RAISE EXCEPTION 'product dependency % would create a cycle', dependency->>'key';
        END IF;
    END LOOP;
    RETURN NEW;
END $$;

CREATE TRIGGER app_product_versions_dependency_guard
    BEFORE UPDATE OF published_at ON app_product_versions
    FOR EACH ROW EXECUTE FUNCTION protect_published_product_dependencies();
CREATE TRIGGER app_product_versions_dependency_insert_guard
    BEFORE INSERT ON app_product_versions
    FOR EACH ROW EXECUTE FUNCTION protect_published_product_dependencies();

COMMIT;
