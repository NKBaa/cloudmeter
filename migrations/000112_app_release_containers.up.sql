CREATE TABLE app_release_containers (
    release_id uuid NOT NULL REFERENCES app_releases(id),
    user_app_id uuid NOT NULL REFERENCES user_apps(id),
    service_key text NOT NULL CHECK (service_key ~ '^[a-z][a-z0-9-]{0,31}$'),
    service_name text NOT NULL CHECK (service_name ~ '^[a-z0-9][a-z0-9-]{0,62}$'),
    container_name text NOT NULL UNIQUE,
    container_id text NOT NULL DEFAULT '',
    is_primary boolean NOT NULL DEFAULT false,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (release_id, service_key)
);
CREATE INDEX app_release_containers_app_release_idx ON app_release_containers(user_app_id, release_id);

INSERT INTO app_release_containers(release_id,user_app_id,service_key,service_name,container_name,container_id,is_primary)
SELECT release.id,release.user_app_id,'primary','primary',route.upstream_container,coalesce(release.container_id,''),true
FROM app_releases release JOIN app_routes route ON route.release_id=release.id AND route.user_app_id=release.user_app_id
WHERE route.upstream_container<>'' ON CONFLICT DO NOTHING;
