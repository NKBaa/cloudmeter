ALTER TABLE app_product_versions
    DROP CONSTRAINT IF EXISTS app_product_versions_image_digest_check;

ALTER TABLE app_product_versions
    ADD CONSTRAINT app_product_versions_image_digest_check CHECK (
        position('://' in image_digest) = 0 AND
        image_digest ~ '^[A-Za-z0-9][A-Za-z0-9._/:-]*(:[A-Za-z0-9_][A-Za-z0-9._-]{0,127}|@sha256:[0-9a-f]{64})$'
    );
