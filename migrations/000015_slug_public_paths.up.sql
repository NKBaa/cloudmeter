BEGIN;

UPDATE app_routes ar
SET public_path = '/apps/' || u.slug || '/' || a.slug
FROM user_apps a
JOIN users u ON u.id = a.user_id
WHERE a.id = ar.user_app_id
  AND ar.public_path <> '/apps/' || u.slug || '/' || a.slug;

COMMIT;
