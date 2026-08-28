BEGIN;

ALTER TABLE system_settings DROP COLUMN privacy_policy;
ALTER TABLE system_settings DROP COLUMN terms_of_service;
ALTER TABLE system_settings DROP COLUMN homepage_content;
ALTER TABLE system_settings DROP COLUMN about_content;
ALTER TABLE system_settings DROP COLUMN footer_text;
ALTER TABLE system_settings DROP COLUMN logo_url;
ALTER TABLE system_settings DROP COLUMN server_url;

COMMIT;
