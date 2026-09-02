ALTER TABLE user_apps DROP CONSTRAINT IF EXISTS user_apps_access_policy_check;
ALTER TABLE user_apps
  DROP COLUMN IF EXISTS access_password_hash,
  DROP COLUMN IF EXISTS access_username,
  DROP COLUMN IF EXISTS access_password_enabled;
