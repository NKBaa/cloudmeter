BEGIN;

CREATE TABLE app_port_mappings (
    user_app_id uuid NOT NULL REFERENCES user_apps(id) ON DELETE CASCADE,
    release_id uuid NOT NULL REFERENCES app_releases(id) ON DELETE CASCADE,
    port_key text NOT NULL CHECK (port_key ~ '^[a-z][a-z0-9-]{0,31}$'),
    container_port integer NOT NULL CHECK (container_port BETWEEN 1 AND 65535),
    host_port integer NOT NULL UNIQUE CHECK (host_port BETWEEN 1 AND 65535),
    remark text NOT NULL DEFAULT '' CHECK (length(remark) <= 120),
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (release_id, port_key),
    UNIQUE (release_id, container_port)
);
CREATE INDEX app_port_mappings_app_release_idx ON app_port_mappings(user_app_id, release_id);

CREATE FUNCTION normalize_selected_release_route(spec jsonb, template jsonb) RETURNS jsonb
LANGUAGE plpgsql IMMUTABLE AS $$
DECLARE
    normalized jsonb := spec; expected jsonb := template; selected_listener jsonb; template_listener jsonb; normalized_listeners jsonb := '[]'::jsonb;
BEGIN
    IF jsonb_typeof(normalized) IS DISTINCT FROM 'object' OR jsonb_typeof(expected) IS DISTINCT FROM 'object' THEN RETURN NULL; END IF;
    IF jsonb_typeof(coalesce(normalized->'listeners','[]'::jsonb)) IS DISTINCT FROM 'array'
       OR jsonb_typeof(coalesce(expected->'listeners','[]'::jsonb)) IS DISTINCT FROM 'array' THEN RETURN NULL; END IF;
    IF jsonb_array_length(coalesce(expected->'listeners','[]'::jsonb))=0 THEN
        normalized := normalized - 'listeners';
        normalized := jsonb_set(normalized,'{containerPort}',expected->'containerPort',true);
        RETURN normalized;
    END IF;
    IF jsonb_array_length(normalized->'listeners')<>jsonb_array_length(expected->'listeners') THEN RETURN NULL; END IF;
    FOR selected_listener IN SELECT value FROM jsonb_array_elements(normalized->'listeners') LOOP
        SELECT value INTO template_listener FROM jsonb_array_elements(expected->'listeners') WHERE value->>'key'=selected_listener->>'key';
        IF template_listener IS NULL
           OR selected_listener->>'remark' IS DISTINCT FROM template_listener->>'remark'
           OR selected_listener->>'primary' IS DISTINCT FROM template_listener->>'primary'
           OR selected_listener->>'userEditable' IS DISTINCT FROM template_listener->>'userEditable'
           OR selected_listener->>'mappingAvailable' IS DISTINCT FROM template_listener->>'mappingAvailable'
           OR (coalesce((template_listener->>'userEditable')::boolean,false)=false AND selected_listener->>'containerPort' IS DISTINCT FROM template_listener->>'containerPort')
           OR (coalesce((selected_listener->>'mappingEnabled')::boolean,false) AND coalesce((template_listener->>'mappingAvailable')::boolean,false)=false)
        THEN RETURN NULL; END IF;
        normalized_listeners := normalized_listeners || jsonb_build_array(template_listener);
        template_listener := NULL;
    END LOOP;
    normalized := jsonb_set(normalized,'{listeners}',normalized_listeners,true);
    normalized := jsonb_set(normalized,'{containerPort}',expected->'containerPort',true);
    RETURN normalized;
EXCEPTION WHEN others THEN RETURN NULL;
END $$;

CREATE OR REPLACE FUNCTION enforce_app_release_parentage() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
    expected_product_slug text; expected_image_digest text; expected_runtime_spec jsonb; expected_route_spec jsonb;
    expected_health_spec jsonb; expected_update_spec jsonb; release_runtime_spec jsonb; declared_key text;
    declared_version_id text; secret_versions jsonb; cloned_snapshot boolean;
BEGIN
    SELECT product.slug,version.image_digest,version.runtime_spec,version.route_spec,version.health_spec,version.update_spec
    INTO expected_product_slug,expected_image_digest,expected_runtime_spec,expected_route_spec,expected_health_spec,expected_update_spec
    FROM user_apps app JOIN app_products product ON product.id=app.product_id
    JOIN app_product_versions version ON version.id=NEW.product_version_id AND version.product_id=app.product_id AND version.published_at IS NOT NULL
    WHERE app.id=NEW.user_app_id;
    IF NOT FOUND THEN RAISE EXCEPTION 'application release product version must belong to the application product'; END IF;
    IF TG_OP='INSERT' AND NEW.state<>'created' THEN RAISE EXCEPTION 'application releases must be inserted in created state'; END IF;
    IF jsonb_typeof(NEW.immutable_snapshot) IS DISTINCT FROM 'object' THEN RAISE EXCEPTION 'application release snapshot must be an object'; END IF;
    IF EXISTS (SELECT 1 FROM jsonb_object_keys(NEW.immutable_snapshot) field(name) WHERE name<>ALL(ARRAY['product_slug','image_digest','runtime_spec','route_spec','health_spec','update_spec','secret_versions'])) THEN RAISE EXCEPTION 'application release snapshot contains an unknown field'; END IF;
    IF TG_OP='UPDATE' THEN RETURN NEW; END IF;
    SELECT EXISTS(SELECT 1 FROM app_releases existing WHERE existing.user_app_id=NEW.user_app_id AND existing.product_version_id=NEW.product_version_id AND existing.immutable_snapshot=NEW.immutable_snapshot) INTO cloned_snapshot;
    IF NOT cloned_snapshot AND (NEW.immutable_snapshot->>'product_slug' IS DISTINCT FROM expected_product_slug
       OR NEW.immutable_snapshot->>'image_digest' IS DISTINCT FROM expected_image_digest
       OR normalize_selected_release_runtime(NEW.immutable_snapshot->'runtime_spec',expected_runtime_spec) IS DISTINCT FROM expected_runtime_spec
       OR normalize_selected_release_route(NEW.immutable_snapshot->'route_spec',expected_route_spec) IS DISTINCT FROM expected_route_spec
       OR NEW.immutable_snapshot->'health_spec' IS DISTINCT FROM expected_health_spec
       OR coalesce(NEW.immutable_snapshot->'update_spec','{"dataPolicy":"volume_compatible"}'::jsonb) IS DISTINCT FROM expected_update_spec)
    THEN RAISE EXCEPTION 'application release snapshot does not match immutable product version'; END IF;
    release_runtime_spec:=coalesce(NEW.immutable_snapshot->'runtime_spec','{}'::jsonb); secret_versions:=coalesce(NEW.immutable_snapshot->'secret_versions','{}'::jsonb);
    IF jsonb_typeof(release_runtime_spec) IS DISTINCT FROM 'object' OR jsonb_typeof(secret_versions) IS DISTINCT FROM 'object' OR jsonb_typeof(coalesce(release_runtime_spec->'secretKeys','[]'::jsonb)) IS DISTINCT FROM 'array' THEN RAISE EXCEPTION 'application release Secret references are invalid'; END IF;
    IF NOT cloned_snapshot AND EXISTS(SELECT 1 FROM jsonb_object_keys(secret_versions) secret(key) WHERE NOT (coalesce(release_runtime_spec->'secretKeys','[]'::jsonb) ? secret.key)) THEN RAISE EXCEPTION 'application release contains an undeclared Secret reference'; END IF;
    FOR declared_key IN SELECT jsonb_array_elements_text(coalesce(release_runtime_spec->'secretKeys','[]'::jsonb)) LOOP
        declared_version_id:=coalesce(secret_versions->>declared_key,'');
        IF declared_version_id !~ '^[0-9a-f]{8}-[0-9a-f]{4}-[1-5][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$' OR NOT EXISTS(SELECT 1 FROM app_secret_versions version JOIN app_secrets secret ON secret.id=version.app_secret_id WHERE version.id=declared_version_id::uuid AND secret.user_app_id=NEW.user_app_id AND secret.key=declared_key) THEN RAISE EXCEPTION 'application release Secret % must reference the same application and key',declared_key; END IF;
    END LOOP;
    RETURN NEW;
END $$;

COMMIT;
