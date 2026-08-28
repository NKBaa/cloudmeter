BEGIN;

-- Migrate data from homepage_settings to system_settings before dropping
UPDATE system_settings
SET homepage_content = (SELECT content_html FROM homepage_settings WHERE singleton LIMIT 1)
WHERE singleton;

DROP TABLE IF EXISTS homepage_settings;

COMMIT;
