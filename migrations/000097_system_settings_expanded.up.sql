BEGIN;

ALTER TABLE system_settings ADD COLUMN server_url text NOT NULL DEFAULT '';
ALTER TABLE system_settings ADD COLUMN logo_url text NOT NULL DEFAULT '';
ALTER TABLE system_settings ADD COLUMN footer_text text NOT NULL DEFAULT '';
ALTER TABLE system_settings ADD COLUMN about_content text NOT NULL DEFAULT '';
ALTER TABLE system_settings ADD COLUMN homepage_content text NOT NULL DEFAULT '';
ALTER TABLE system_settings ADD COLUMN terms_of_service text NOT NULL DEFAULT '';
ALTER TABLE system_settings ADD COLUMN privacy_policy text NOT NULL DEFAULT '';

COMMIT;
