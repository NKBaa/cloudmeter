BEGIN;

ALTER TABLE app_product_versions
    ADD COLUMN update_spec jsonb NOT NULL DEFAULT '{"dataPolicy":"volume_compatible"}'::jsonb,
    ADD CONSTRAINT app_product_versions_update_spec_check CHECK (
        jsonb_typeof(update_spec) = 'object'
        AND update_spec->>'dataPolicy' IN ('stateless','volume_compatible','backup_required')
    );

COMMIT;
