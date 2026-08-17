BEGIN;

-- Legacy templates without explicit limits ran before resource entitlements
-- were enforced. Give them bounded defaults before requiring every new
-- product version to declare CPU and memory reservations.
UPDATE app_product_versions
SET runtime_spec = jsonb_set(
    jsonb_set(runtime_spec, '{cpuCores}', coalesce(runtime_spec->'cpuCores', '1'::jsonb), true),
    '{memoryMiB}', coalesce(runtime_spec->'memoryMiB', '512'::jsonb), true
);

ALTER TABLE app_product_versions ADD CONSTRAINT app_product_versions_runtime_resources_check CHECK (
    jsonb_typeof(runtime_spec->'cpuCores') = 'number'
    AND (runtime_spec->>'cpuCores')::numeric BETWEEN 0.1 AND 64
    AND jsonb_typeof(runtime_spec->'memoryMiB') = 'number'
    AND (runtime_spec->>'memoryMiB')::numeric BETWEEN 64 AND 262144
);

COMMIT;
