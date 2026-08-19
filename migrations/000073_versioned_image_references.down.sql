ALTER TABLE app_product_versions
    DROP CONSTRAINT IF EXISTS app_product_versions_image_digest_check;

ALTER TABLE app_product_versions
    ADD CONSTRAINT app_product_versions_image_digest_check CHECK (
        image_digest ~ '^.+@sha256:[0-9a-f]{64}$'
    );
