ALTER TABLE user_apps
  ADD COLUMN access_password_enabled boolean NOT NULL DEFAULT false,
  ADD COLUMN access_username text NOT NULL DEFAULT '',
  ADD COLUMN access_password_hash text NOT NULL DEFAULT '';

ALTER TABLE user_apps
  ADD CONSTRAINT user_apps_access_policy_check CHECK (
    (NOT access_password_enabled AND access_username = '' AND access_password_hash = '')
    OR
    (access_password_enabled AND length(access_username) BETWEEN 1 AND 64 AND access_password_hash <> '')
  );
