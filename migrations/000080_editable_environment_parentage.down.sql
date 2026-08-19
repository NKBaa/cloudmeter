BEGIN;

-- Restore the 000079 implementation exactly when rolling back this fix.
CREATE OR REPLACE FUNCTION normalize_selected_release_runtime(spec jsonb, template jsonb) RETURNS jsonb
LANGUAGE plpgsql IMMUTABLE AS $$
DECLARE
    normalized jsonb := normalize_release_runtime_spec(spec);
    expected jsonb := normalize_release_runtime_spec(template);
    options jsonb := coalesce(template->'editableOptions','{}'::jsonb);
    release_env jsonb := coalesce(normalized->'env','{}'::jsonb);
    template_env jsonb := coalesce(expected->'env','{}'::jsonb);
    editable_env jsonb := coalesce(template->'editableEnvKeys','[]'::jsonb);
    env_key text;
    release_volume jsonb;
    expected_volume jsonb;
    release_volumes jsonb;
BEGIN
    IF normalized IS NULL OR expected IS NULL OR jsonb_typeof(options) IS DISTINCT FROM 'object'
       OR jsonb_typeof(release_env) IS DISTINCT FROM 'object'
       OR jsonb_typeof(editable_env) IS DISTINCT FROM 'array' THEN
        RETURN NULL;
    END IF;
    IF options->>'cpu' = 'true' THEN
        IF jsonb_typeof(normalized->'cpuCores') <> 'number'
           OR (normalized->>'cpuCores')::numeric < (expected->>'cpuCores')::numeric THEN RETURN NULL; END IF;
        normalized := jsonb_set(normalized,'{cpuCores}',expected->'cpuCores',true);
    END IF;
    IF options->>'memory' = 'true' THEN
        IF jsonb_typeof(normalized->'memoryMiB') <> 'number'
           OR (normalized->>'memoryMiB')::numeric < (expected->>'memoryMiB')::numeric THEN RETURN NULL; END IF;
        normalized := jsonb_set(normalized,'{memoryMiB}',expected->'memoryMiB',true);
    END IF;
    IF options->>'dataVolume' = 'true' THEN
        IF jsonb_array_length(coalesce(expected->'volumes','[]'::jsonb)) = 0 THEN
            IF normalized ? 'dataVolumeGiB' THEN RETURN NULL; END IF;
        ELSE
            IF jsonb_typeof(normalized->'dataVolumeGiB') <> 'number'
               OR (normalized->>'dataVolumeGiB')::numeric < (expected->>'dataVolumeGiB')::numeric THEN RETURN NULL; END IF;
            release_volumes := '[]'::jsonb;
            FOR release_volume IN SELECT value FROM jsonb_array_elements(normalized->'volumes') LOOP
                SELECT value INTO expected_volume FROM jsonb_array_elements(expected->'volumes') WHERE value->>'name'=release_volume->>'name';
                IF expected_volume IS NULL OR release_volume->>'mountPath' IS DISTINCT FROM expected_volume->>'mountPath'
                   OR jsonb_typeof(release_volume->'sizeGiB') <> 'number'
                   OR (release_volume->>'sizeGiB')::numeric <> (normalized->>'dataVolumeGiB')::numeric THEN RETURN NULL; END IF;
                release_volumes := release_volumes || jsonb_build_array(jsonb_set(release_volume,'{sizeGiB}',expected_volume->'sizeGiB',true));
                expected_volume := NULL;
            END LOOP;
            normalized := jsonb_set(normalized,'{volumes}',release_volumes,true);
            normalized := jsonb_set(normalized,'{dataVolumeGiB}',expected->'dataVolumeGiB',true);
        END IF;
    END IF;
    IF options->>'command' = 'true' THEN
        IF normalized ? 'command' AND jsonb_typeof(normalized->'command') <> 'array' THEN RETURN NULL; END IF;
        IF expected ? 'command' THEN normalized := jsonb_set(normalized,'{command}',expected->'command',true);
        ELSE normalized := normalized - 'command'; END IF;
    END IF;
    FOR env_key IN SELECT jsonb_array_elements_text(editable_env) LOOP
        IF NOT (release_env ? env_key) OR jsonb_typeof(release_env->env_key) <> 'string' THEN RETURN NULL; END IF;
        template_env := jsonb_set(template_env,ARRAY[env_key],release_env->env_key,true);
    END LOOP;
    normalized := jsonb_set(normalized,'{env}',template_env,true);
    IF options->>'dependencies' = 'true' THEN
        IF normalized ? 'dependencies' AND jsonb_typeof(normalized->'dependencies') <> 'array' THEN RETURN NULL; END IF;
        IF expected ? 'dependencies' THEN normalized := jsonb_set(normalized,'{dependencies}',expected->'dependencies',true);
        ELSE normalized := normalized - 'dependencies'; END IF;
    END IF;
    RETURN normalized;
EXCEPTION WHEN others THEN
    RETURN NULL;
END $$;

COMMIT;
