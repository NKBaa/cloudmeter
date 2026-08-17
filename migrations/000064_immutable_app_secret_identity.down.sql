BEGIN;
DROP TRIGGER IF EXISTS app_secrets_no_truncate ON app_secrets;
DROP TRIGGER IF EXISTS app_secrets_identity_immutable ON app_secrets;
DROP FUNCTION IF EXISTS protect_app_secret_identity;
COMMIT;
