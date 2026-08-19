BEGIN;
ALTER TABLE app_products ADD COLUMN deleted_at timestamptz;
ALTER TABLE user_apps ADD COLUMN deleted_at timestamptz;
ALTER TABLE app_product_versions ADD COLUMN version_label text NOT NULL DEFAULT '';
ALTER TABLE app_product_versions ADD CONSTRAINT app_product_versions_label_check CHECK (length(version_label) <= 64);
ALTER TABLE app_products DROP CONSTRAINT app_products_slug_key;
CREATE UNIQUE INDEX app_products_slug_key ON app_products(slug) WHERE deleted_at IS NULL;
ALTER TABLE user_apps DROP CONSTRAINT user_apps_user_id_slug_key;
ALTER TABLE user_apps DROP CONSTRAINT user_apps_user_id_service_slug_key;
CREATE UNIQUE INDEX user_apps_user_id_slug_key ON user_apps(user_id,slug) WHERE deleted_at IS NULL;
CREATE UNIQUE INDEX user_apps_user_id_service_slug_key ON user_apps(user_id,service_slug) WHERE deleted_at IS NULL;
CREATE TABLE app_deletion_jobs (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_app_id uuid NOT NULL REFERENCES user_apps(id),
    status text NOT NULL DEFAULT 'queued' CHECK (status IN ('queued','running','succeeded')),
    attempts integer NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    available_at timestamptz NOT NULL DEFAULT now(),
    last_error text,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    completed_at timestamptz,
    UNIQUE (user_app_id),
    CHECK ((status = 'succeeded') = (completed_at IS NOT NULL))
);
CREATE INDEX app_deletion_jobs_queue_idx ON app_deletion_jobs(status,available_at,created_at);
CREATE INDEX app_products_active_idx ON app_products(created_at DESC) WHERE deleted_at IS NULL;
CREATE INDEX user_apps_active_user_idx ON user_apps(user_id,created_at DESC) WHERE deleted_at IS NULL;
COMMIT;
